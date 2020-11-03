package lifecycle_test

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"

	"github.com/buildpacks/lifecycle"
	"github.com/buildpacks/lifecycle/launch"
	"github.com/buildpacks/lifecycle/layers"
	h "github.com/buildpacks/lifecycle/testhelpers"
	"github.com/buildpacks/lifecycle/testmock"
)

const latestBuildpackAPI = "0.5" // TODO: is there a good way to ensure this is kept up to date?

func TestBuildpackTOML(t *testing.T) {
	spec.Run(t, "DefaultBuildpackTOML", testBuildpackTOML, spec.Report(report.Terminal{}))
}

//go:generate mockgen -package testmock -destination testmock/env.go github.com/buildpacks/lifecycle BuildEnv

func testBuildpackTOML(t *testing.T, when spec.G, it spec.S) {
	var (
		bpTOML         lifecycle.DefaultBuildpackTOML
		mockCtrl       *gomock.Controller
		env            *testmock.MockBuildEnv
		stdout, stderr *bytes.Buffer
		tmpDir         string
		platformDir    string
		appDir         string
		layersDir      string
		buildpacksDir  string
		config         lifecycle.BuildConfig
	)

	it.Before(func() {
		mockCtrl = gomock.NewController(t)
		env = testmock.NewMockBuildEnv(mockCtrl)

		var err error
		tmpDir, err = ioutil.TempDir("", "lifecycle")
		if err != nil {
			t.Fatalf("Error: %s\n", err)
		}
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
		platformDir = filepath.Join(tmpDir, "platform")
		layersDir = filepath.Join(tmpDir, "launch")
		appDir = filepath.Join(layersDir, "app")
		mkdir(t, layersDir, appDir, filepath.Join(platformDir, "env"))

		buildpacksDir, err = filepath.Abs(filepath.Join("testdata", "by-id"))
		if err != nil {
			t.Fatalf("Error: %s\n", err)
		}

		config = lifecycle.BuildConfig{
			Env:         env,
			AppDir:      appDir,
			PlatformDir: platformDir,
			LayersDir:   layersDir,
			PlanDir:     appDir, // TODO: check if this makes sense
			Out:         stdout,
			Err:         stderr,
		}

		bpTOML = lifecycle.DefaultBuildpackTOML{
			API: latestBuildpackAPI,
			Buildpack: lifecycle.BuildpackInfo{
				ID:       "A",
				Version:  "v1",
				Name:     "Buildpack A",
				ClearEnv: false,
				Homepage: "Buildpack A Homepage",
			},
			Path: filepath.Join(buildpacksDir, "A", "v1"), // TODO: avoid doubly specifying this information
		}
	})

	it.After(func() {
		os.RemoveAll(tmpDir)
		mockCtrl.Finish()
	})

	when("#Build", func() {
		when("building succeeds", func() {
			it.Before(func() {
				env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
			})

			it("should ensure the buildpack's layers dir exists and process build layers", func() {
				mkdir(t,
					filepath.Join(layersDir, "A"),
					filepath.Join(appDir, "layers-A-v1", "layer1"),
					filepath.Join(appDir, "layers-A-v1", "layer2"),
					filepath.Join(appDir, "layers-A-v1", "layer3"),
				)
				mkfile(t, "build = true",
					filepath.Join(appDir, "layers-A-v1", "layer1.toml"),
					filepath.Join(appDir, "layers-A-v1", "layer3.toml"),
				)
				gomock.InOrder(
					env.EXPECT().AddRootDir(filepath.Join(layersDir, "A", "layer1")),
					env.EXPECT().AddRootDir(filepath.Join(layersDir, "A", "layer3")),
					env.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer1", "env")),
					env.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer1", "env.build")),
					env.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer3", "env")),
					env.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer3", "env.build")),
				)
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				testExists(t,
					filepath.Join(layersDir, "A"),
				)
			})

			it("should provide the platform dir", func() {
				mkfile(t, "some-data",
					filepath.Join(platformDir, "env", "SOME_VAR"),
				)
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				testExists(t,
					filepath.Join(appDir, "build-env-A-v1", "SOME_VAR"),
				)
			})

			it("should provide environment variables", func() {
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(rdfile(t, filepath.Join(appDir, "build-info-A-v1")),
					"TEST_ENV: Av1\n",
				); s != "" {
					t.Fatalf("Unexpected info:\n%s\n", s)
				}
			})

			it("should set CNB_BUILDPACK_DIR", func() {
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(rdfile(t, filepath.Join(appDir, "build-env-cnb-buildpack-dir-A-v1")),
					filepath.Join(bpTOML.Path),
				); s != "" {
					t.Fatalf("Unexpected CNB_BUILDPACK_DIR:\n%s\n", s)
				}
			})

			it("should connect stdout and stdin to the terminal", func() {
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(cleanEndings(stdout.String()), "build out: A@v1\n"); s != "" {
					t.Fatalf("Unexpected stdout:\n%s\n", s)
				}
				if s := cmp.Diff(cleanEndings(stderr.String()), "build err: A@v1\n"); s != "" {
					t.Fatalf("Unexpected stderr:\n%s\n", s)
				}
			})

			when("build result", func() {
				it("should get bom entries from launch.toml and unmet requires from build.toml", func() {
					bpPlan := lifecycle.BuildpackPlan{
						Entries: []lifecycle.Require{
							{
								Name:    "some-deprecated-bp-replace-version-dep",
								Version: "some-version-orig", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:     "some-dep",
								Metadata: map[string]interface{}{"version": "v1"},
							},
							{
								Name:     "some-replace-version-dep",
								Metadata: map[string]interface{}{"version": "some-version-orig"},
							},
							{
								Name: "some-unmet-dep",
							},
						},
					}

					mkfile(t,
						"[[bom]]\n"+
							`name = "some-deprecated-bp-replace-version-dep"`+"\n"+
							"[bom.metadata]\n"+
							`version = "some-version-new"`+"\n"+
							"[[bom]]\n"+
							`name = "some-dep"`+"\n"+
							"[bom.metadata]\n"+
							`version = "v1"`+"\n"+
							"[[bom]]\n"+
							`name = "some-replace-version-dep"`+"\n"+
							"[bom.metadata]\n"+
							`version = "some-version-new"`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)

					mkfile(t,
						"[[unmet]]\n"+
							`name = "some-unmet-dep"`+"\n",
						filepath.Join(appDir, "build-A-v1.toml"),
					)

					br, err := bpTOML.Build(bpPlan, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, lifecycle.BuildResult{
						BOM: []lifecycle.BOMEntry{
							{
								Require: lifecycle.Require{
									Name:     "some-deprecated-bp-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"}, // no api, no homepage
							},
							{
								Require: lifecycle.Require{
									Name:     "some-dep",
									Metadata: map[string]interface{}{"version": "v1"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"}, // no api, no homepage
							},
							{
								Require: lifecycle.Require{
									Name:     "some-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"}, // no api, no homepage
							},
						},
						Labels:    []lifecycle.Label{},
						Met:       []string{"some-deprecated-bp-replace-version-dep", "some-dep", "some-replace-version-dep"},
						Processes: []launch.Process{},
						Slices:    []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})

				it("should include labels", func() {
					mkfile(t,
						"[[labels]]\n"+
							`key = "some-key"`+"\n"+
							`value = "some-value"`+"\n"+
							"[[labels]]\n"+
							`key = "some-other-key"`+"\n"+
							`value = "some-other-value"`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)

					br, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, lifecycle.BuildResult{
						BOM: nil,
						Labels: []lifecycle.Label{
							{Key: "some-key", Value: "some-value"},
							{Key: "some-other-key", Value: "some-other-value"},
						},
						Met:       nil,
						Processes: []launch.Process{},
						Slices:    []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})

				it("should include processes", func() {
					mkfile(t,
						`[[processes]]`+"\n"+
							`type = "some-type"`+"\n"+
							`command = "some-cmd"`+"\n"+
							`[[processes]]`+"\n"+
							`type = "other-type"`+"\n"+
							`command = "other-cmd"`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)
					br, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}
					if s := cmp.Diff(br, lifecycle.BuildResult{
						BOM:    nil, // TODO: fix
						Labels: []lifecycle.Label{},
						Met:    nil, // TODO: fix
						Processes: []launch.Process{
							{Type: "some-type", Command: "some-cmd", BuildpackID: "A"},
							{Type: "other-type", Command: "other-cmd", BuildpackID: "A"},
						},
						Slices: []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected metadata:\n%s\n", s)
					}
				})

				it("should include slices", func() {
					mkfile(t,
						"[[slices]]\n"+
							`paths = ["some-path", "some-other-path"]`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)

					br, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, lifecycle.BuildResult{
						BOM:       nil,
						Labels:    []lifecycle.Label{},
						Met:       nil,
						Processes: []launch.Process{},
						Slices:    []layers.Slice{{Paths: []string{"some-path", "some-other-path"}}},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})
			})
		})

		when("building succeeds with a clear env", func() {
			it.Before(func() {
				env.EXPECT().List().Return(append(os.Environ(), "TEST_ENV=cleared"))

				bpTOML.Buildpack.Version = "v1.clear"
				bpTOML.Path = filepath.Join(buildpacksDir, "A", "v1.clear")
				bpTOML.Buildpack.ClearEnv = true // TODO: it is weird that all three of these conditions need to be specified
			})

			it("should not apply user-provided env vars", func() {
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				if s := cmp.Diff(rdfile(t, filepath.Join(appDir, "build-info-A-v1.clear")),
					"TEST_ENV: cleared\n",
				); s != "" {
					t.Fatalf("Unexpected info:\n%s\n", s)
				}
			})

			it("should set CNB_BUILDPACK_DIR", func() {
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				bpsDir, err := filepath.Abs(buildpacksDir) // TODO: this should be passed in somehow
				if err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(rdfile(t, filepath.Join(appDir, "build-env-cnb-buildpack-dir-A-v1.clear")),
					filepath.Join(bpsDir, "A/v1.clear"),
				); s != "" {
					t.Fatalf("Unexpected CNB_BUILDPACK_DIR:\n%s\n", s)
				}
			})
		})

		when("building fails", func() {
			it("should error when layer directories cannot be created", func() {
				mkfile(t, "some-data", filepath.Join(layersDir, "A"))
				_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
				if _, ok := err.(*os.PathError); !ok {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			it("should error when the provided buildpack plan is invalid", func() {
				bpPlan := lifecycle.BuildpackPlan{
					Entries: []lifecycle.Require{
						{
							Metadata: map[string]interface{}{"a": map[int64]int64{1: 2}}, // map with non-string key type
						},
					},
				}
				if _, err := bpTOML.Build(bpPlan, config); err == nil {
					t.Fatal("Expected error.\n")
				} else if !strings.Contains(err.Error(), "toml") {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			it("should error when the env cannot be found", func() {
				env.EXPECT().WithPlatform(platformDir).Return(nil, errors.New("some error"))
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err == nil {
					t.Fatal("Expected error.\n")
				} else if !strings.Contains(err.Error(), "some error") {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			it("should error when the command fails", func() {
				env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				if err := os.RemoveAll(platformDir); err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
				if err, ok := err.(*lifecycle.Error); !ok || err.Type != lifecycle.ErrTypeBuildpack {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			when("modifying the env fails", func() {
				var appendErr error

				it.Before(func() {
					appendErr = errors.New("some error")
				})

				each(it, []func(){
					func() {
						env.EXPECT().AddRootDir(gomock.Any()).Return(appendErr)
					},
					func() {
						env.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						env.EXPECT().AddRootDir(gomock.Any()).Return(appendErr)
					},
					func() {
						env.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						env.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						env.EXPECT().AddEnvDir(gomock.Any()).Return(appendErr)
					},
					func() {
						env.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						env.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						env.EXPECT().AddEnvDir(gomock.Any()).Return(nil)
						env.EXPECT().AddEnvDir(gomock.Any()).Return(appendErr)
					},
				}, "should error", func() {
					env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
					mkdir(t,
						filepath.Join(appDir, "layers-A-v1", "layer1"),
						filepath.Join(appDir, "layers-A-v1", "layer2"),
					)
					mkfile(t, "build = true",
						filepath.Join(appDir, "layers-A-v1", "layer1.toml"),
						filepath.Join(appDir, "layers-A-v1", "layer2.toml"),
					)
					if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err != appendErr {
						t.Fatalf("Incorrect error: %s\n", err)
					}
				})
			})

			it("should error when launch.toml is not writable", func() {
				env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				mkdir(t, filepath.Join(layersDir, "A", "launch.toml"))
				if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err == nil {
					t.Fatal("Expected error")
				}
			})

			it("should error when the build bom has a top level version", func() {
				env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				mkfile(t,
					"[[bom]]\n"+
						`name = "some-dep"`+"\n"+
						`version = "some-version"`+"\n",
					filepath.Join(appDir, "build-A-v1.toml"),
				)
				_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
				h.AssertNotNil(t, err)
				expected := "top level version which is deprecated"
				h.AssertStringContains(t, err.Error(), expected)
			})

			it("should error when the launch bom has a top level version", func() {
				env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				mkfile(t,
					"[[bom]]\n"+
						`name = "some-dep"`+"\n"+
						`version = "some-version"`+"\n",
					filepath.Join(appDir, "launch-A-v1.toml"),
				)
				_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
				h.AssertNotNil(t, err)
				expected := "top level version which is deprecated"
				h.AssertStringContains(t, err.Error(), expected)
			})

			when("invalid unmet entries", func() {
				when("missing name", func() {
					it("should error", func() {
						env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
						mkfile(t,
							"[[unmet]]\n",
							filepath.Join(appDir, "build-A-v1.toml"),
						)
						_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
						h.AssertNotNil(t, err)
						expected := "name is required"
						h.AssertStringContains(t, err.Error(), expected)
					})
				})

				when("invalid name", func() {
					it("should error", func() {
						env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
						mkfile(t,
							"[[unmet]]\n"+
								`name = "unknown-dep"`+"\n",
							filepath.Join(appDir, "build-A-v1.toml"),
						)
						_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
						h.AssertNotNil(t, err)
						expected := "must match a requested dependency"
						h.AssertStringContains(t, err.Error(), expected)
					})
				})
			})
		})

		when("buildpack api = 0.2", func() {
			it.Before(func() {
				bpTOML.API = "0.2"
				env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
			})

			it("should convert metadata version to top level version in the buildpack plan", func() {
				bpPlan := lifecycle.BuildpackPlan{
					Entries: []lifecycle.Require{
						{
							Name:     "some-dep",
							Metadata: map[string]interface{}{"version": "v1"},
						},
					},
				}

				_, err := bpTOML.Build(bpPlan, config)
				if err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}

				testPlan(t,
					[]lifecycle.Require{
						{
							Name:     "some-dep",
							Version:  "v1",
							Metadata: map[string]interface{}{"version": "v1"},
						},
					},
					filepath.Join(appDir, "build-plan-in-A-v1.toml"),
				)
			})
		})

		when("buildpack api < 0.5", func() {
			it.Before(func() {
				bpTOML.API = "0.4"
			})

			when("building succeeds", func() {
				it.Before(func() {
					env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				})

				it("should get bom entries and unmet requires from the buildpack plan", func() {
					bpPlan := lifecycle.BuildpackPlan{
						Entries: []lifecycle.Require{
							{
								Name:    "some-deprecated-bp-dep",
								Version: "v1", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:    "some-deprecated-bp-replace-version-dep",
								Version: "some-version-orig", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:    "some-deprecated-bp-add-version-dep",
								Version: "v2", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:    "some-deprecated-bp-move-version-dep",
								Version: "v3", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:     "some-dep",
								Metadata: map[string]interface{}{"version": "v1"},
							},
							{
								Name:     "some-replace-version-dep",
								Metadata: map[string]interface{}{"version": "some-version-orig"},
							},
							{
								Name: "some-unmet-dep",
							},
						},
					}

					mkfile(t,
						"[[entries]]\n"+
							`name = "some-deprecated-bp-dep"`+"\n"+
							`version = "v1"`+"\n"+
							"[[entries]]\n"+
							`name = "some-deprecated-bp-replace-version-dep"`+"\n"+
							`version = "some-version-new"`+"\n"+
							"[[entries]]\n"+
							`name = "some-deprecated-bp-add-version-dep"`+"\n"+
							`version = "v2"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v2"`+"\n"+
							"[[entries]]\n"+
							`name = "some-deprecated-bp-move-version-dep"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v3"`+"\n"+
							"[[entries]]\n"+
							`name = "some-dep"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v1"`+"\n"+
							"[[entries]]\n"+
							`name = "some-replace-version-dep"`+"\n"+
							"[entries.metadata]\n"+
							`version = "some-version-new"`+"\n",
						filepath.Join(appDir, "build-plan-out-A-v1.toml"),
					)

					br, err := bpTOML.Build(bpPlan, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, lifecycle.BuildResult{
						BOM: []lifecycle.BOMEntry{
							{
								Require: lifecycle.Require{
									Name:    "some-deprecated-bp-dep",
									Version: "v1",
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"},
							},
							{
								Require: lifecycle.Require{
									Name:    "some-deprecated-bp-replace-version-dep",
									Version: "some-version-new",
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"},
							},
							{
								Require: lifecycle.Require{
									Name:     "some-deprecated-bp-add-version-dep",
									Version:  "v2",
									Metadata: map[string]interface{}{"version": "v2"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"},
							},
							{
								Require: lifecycle.Require{
									Name:     "some-deprecated-bp-move-version-dep",
									Metadata: map[string]interface{}{"version": "v3"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"},
							},
							{
								Require: lifecycle.Require{
									Name:     "some-dep",
									Metadata: map[string]interface{}{"version": "v1"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"},
							},
							{
								Require: lifecycle.Require{
									Name:     "some-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: lifecycle.Buildpack{ID: "A", Version: "v1"},
							},
						},
						Labels: []lifecycle.Label{},
						Met: []string{
							"some-deprecated-bp-dep",
							"some-deprecated-bp-replace-version-dep",
							"some-deprecated-bp-add-version-dep",
							"some-deprecated-bp-move-version-dep",
							"some-dep",
							"some-replace-version-dep",
						},
						Processes: []launch.Process{},
						Slices:    []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})
			})

			when("building fails", func() {
				it("should error when the output buildpack plan is invalid", func() {
					env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
					mkfile(t, "bad-key", filepath.Join(appDir, "build-plan-out-A-v1.toml"))
					if _, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config); err == nil {
						t.Fatal("Expected error.\n")
					} else if !strings.Contains(err.Error(), "key") {
						t.Fatalf("Incorrect error: %s\n", err)
					}
				})

				it("should error when top level version and metadata version are both present and do not match", func() {
					env.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
					mkfile(t,
						"[[entries]]\n"+
							`name = "dep1"`+"\n"+
							`version = "v2"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v1"`+"\n",
						filepath.Join(appDir, "build-plan-out-A-v1.toml"),
					)
					_, err := bpTOML.Build(lifecycle.BuildpackPlan{}, config)
					h.AssertNotNil(t, err)
					expected := "top level version does not match metadata version"
					h.AssertStringContains(t, err.Error(), expected)
				})
			})
		})
	})
}
