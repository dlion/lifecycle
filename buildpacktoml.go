package lifecycle

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/buildpacks/lifecycle/api"
	"github.com/buildpacks/lifecycle/launch"
	"github.com/buildpacks/lifecycle/layers"
)

type BuildTOML struct {
	BOM   []BOMEntry `toml:"bom"`
	Unmet []Unmet    `toml:"unmet"`
}

type Unmet struct {
	Name string `toml:"name"`
}

type LaunchTOML struct {
	BOM       []BOMEntry
	Labels    []Label
	Processes []launch.Process `toml:"processes"`
	Slices    []layers.Slice   `toml:"slices"`
}

type DefaultBuildpackTOML struct {
	API       string         `toml:"api"`
	Buildpack BuildpackInfo  `toml:"buildpack"`
	Order     BuildpackOrder `toml:"order"`
	Path      string         `toml:"-"`
}

func (b DefaultBuildpackTOML) String() string {
	return b.Buildpack.Name + " " + b.Buildpack.Version
}

func (b *DefaultBuildpackTOML) Build(bpPlan BuildpackPlan, config BuildConfig) (BuildResult, error) {
	if api.MustParse(b.API).Equal(api.MustParse("0.2")) {
		for i := range bpPlan.Entries {
			bpPlan.Entries[i].convertMetadataToVersion()
		}
	}

	bpLayersDir, bpPlanPath, err := preparePaths(b.Buildpack.ID, bpPlan, config.LayersDir, config.PlanDir)
	if err != nil {
		return BuildResult{}, err
	}

	if err := b.runBuildCmd(bpLayersDir, bpPlanPath, config); err != nil {
		return BuildResult{}, err
	}

	if err := setupEnv(config.Env, bpLayersDir); err != nil {
		return BuildResult{}, err
	}

	return b.readOutputFiles(bpLayersDir, bpPlanPath, bpPlan)
}

func preparePaths(bpID string, bpPlan BuildpackPlan, layersDir, planDir string) (string, string, error) {
	bpDirName := launch.EscapeID(bpID)
	bpLayersDir := filepath.Join(layersDir, bpDirName)
	bpPlanDir := filepath.Join(planDir, bpDirName)
	if err := os.MkdirAll(bpLayersDir, 0777); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(bpPlanDir, 0777); err != nil {
		return "", "", err
	}
	bpPlanPath := filepath.Join(bpPlanDir, "plan.toml")
	if err := WriteTOML(bpPlanPath, bpPlan); err != nil {
		return "", "", err
	}

	return bpLayersDir, bpPlanPath, nil
}

func (b *DefaultBuildpackTOML) runBuildCmd(bpLayersDir, bpPlanPath string, config BuildConfig) error {
	cmd := exec.Command(
		filepath.Join(b.Path, "bin", "build"),
		bpLayersDir,
		config.PlatformDir,
		bpPlanPath,
	)
	cmd.Dir = config.AppDir
	cmd.Stdout = config.Out
	cmd.Stderr = config.Err

	var err error
	if b.Buildpack.ClearEnv {
		cmd.Env = config.Env.List()
	} else {
		cmd.Env, err = config.Env.WithPlatform(config.PlatformDir)
		if err != nil {
			return err
		}
	}
	cmd.Env = append(cmd.Env, EnvBuildpackDir+"="+b.Path)

	if err := cmd.Run(); err != nil {
		return NewLifecycleError(err, ErrTypeBuildpack)
	}
	return nil
}

func setupEnv(env BuildEnv, layersDir string) error {
	if err := eachDir(layersDir, func(path string) error {
		if !isBuild(path + ".toml") {
			return nil
		}
		return env.AddRootDir(path)
	}); err != nil {
		return err
	}

	return eachDir(layersDir, func(path string) error {
		if !isBuild(path + ".toml") {
			return nil
		}
		if err := env.AddEnvDir(filepath.Join(path, "env")); err != nil {
			return err
		}
		return env.AddEnvDir(filepath.Join(path, "env.build"))
	})
}

func eachDir(dir string, fn func(path string) error) error {
	files, err := ioutil.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		if err := fn(filepath.Join(dir, f.Name())); err != nil {
			return err
		}
	}
	return nil
}

func isBuild(path string) bool {
	var layerTOML struct {
		Build bool `toml:"build"`
	}
	_, err := toml.DecodeFile(path, &layerTOML)
	return err == nil && layerTOML.Build
}

func (b *DefaultBuildpackTOML) readOutputFiles(bpLayersDir, bpPlanPath string, bpPlanIn BuildpackPlan) (BuildResult, error) {
	br := BuildResult{}
	bpFromBpInfo := Buildpack{ID: b.Buildpack.ID, Version: b.Buildpack.Version}

	// read launch.toml
	var launchTOML LaunchTOML
	launchPath := filepath.Join(bpLayersDir, "launch.toml")
	if _, err := toml.DecodeFile(launchPath, &launchTOML); err != nil && !os.IsNotExist(err) {
		return BuildResult{}, err
	}

	if api.MustParse(b.API).Compare(api.MustParse("0.5")) < 0 { // buildpack API <= 0.4
		// read buildpack plan
		var bpPlanOut BuildpackPlan
		if _, err := toml.DecodeFile(bpPlanPath, &bpPlanOut); err != nil {
			return BuildResult{}, err
		}
		if err := validateBOM(bpPlanOut.toBOM(), b.API); err != nil {
			return BuildResult{}, err
		}
		br.BOM = withBuildpack(bpFromBpInfo, bpPlanOut.toBOM())
		br.Met = names(bpPlanOut.Entries)
	} else {
		// read build.toml
		var bpBuild BuildTOML
		buildPath := filepath.Join(bpLayersDir, "build.toml")
		if _, err := toml.DecodeFile(buildPath, &bpBuild); err != nil && !os.IsNotExist(err) {
			return BuildResult{}, err
		}
		if err := validateBOM(launchTOML.BOM, b.API); err != nil {
			return BuildResult{}, err
		}
		if err := validateBOM(bpBuild.BOM, b.API); err != nil { // TODO: maybe this validation should happen in exporter
			return BuildResult{}, err
		}
		if err := validateUnmet(bpBuild.Unmet, bpPlanIn); err != nil {
			return BuildResult{}, err
		}
		br.BOM = withBuildpack(bpFromBpInfo, launchTOML.BOM)
		br.Met = names(bpPlanIn.filter(bpBuild.Unmet).Entries)
	}

	br.Labels = append([]Label{}, launchTOML.Labels...)
	for i := range launchTOML.Processes {
		launchTOML.Processes[i].BuildpackID = b.Buildpack.ID
	}
	br.Processes = append([]launch.Process{}, launchTOML.Processes...)
	br.Slices = append([]layers.Slice{}, launchTOML.Slices...)

	return br, nil
}

func validateBOM(bom []BOMEntry, bpAPI string) error {
	if api.MustParse(bpAPI).Compare(api.MustParse("0.5")) < 0 {
		for _, entry := range bom {
			if version, ok := entry.Metadata["version"]; ok {
				metadataVersion := fmt.Sprintf("%v", version)
				if entry.Version != "" && entry.Version != metadataVersion {
					return errors.New("top level version does not match metadata version")
				}
			}
		}
	} else {
		for _, entry := range bom {
			if entry.Version != "" {
				return fmt.Errorf("bom entry '%s' has a top level version which is deprecated", entry.Name)
			}
		}
	}
	return nil
}

func validateUnmet(unmet []Unmet, bpPlan BuildpackPlan) error {
	for _, unmet := range unmet {
		if unmet.Name == "" {
			return errors.New("unmet.name is required")
		}
		found := false
		for _, req := range bpPlan.Entries {
			if unmet.Name == req.Name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unmet.name '%s' must match a requested dependency", unmet.Name)
		}
	}
	return nil
}

func names(requires []Require) []string {
	var out []string
	for _, req := range requires {
		out = append(out, req.Name)
	}
	return out
}

func (p BuildpackPlan) filter(unmet []Unmet) BuildpackPlan {
	var out []Require
	for _, entry := range p.Entries {
		if !containsName(unmet, entry.Name) {
			out = append(out, entry)
		}
	}
	return BuildpackPlan{Entries: out}
}

func containsName(unmet []Unmet, name string) bool {
	for _, u := range unmet {
		if u.Name == name {
			return true
		}
	}
	return false
}

func (p BuildpackPlan) toBOM() []BOMEntry {
	var bom []BOMEntry
	for _, entry := range p.Entries {
		bom = append(bom, BOMEntry{Require: entry})
	}
	return bom
}

func withBuildpack(bp Buildpack, bom []BOMEntry) []BOMEntry {
	var out []BOMEntry
	for _, entry := range bom {
		entry.Buildpack = bp.noAPI().noHomepage()
		out = append(out, entry)
	}
	return out
}
