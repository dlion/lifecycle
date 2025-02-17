package platform

import (
	"errors"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"

	"github.com/buildpacks/lifecycle/auth"
	"github.com/buildpacks/lifecycle/cmd"
	iname "github.com/buildpacks/lifecycle/internal/name"
	"github.com/buildpacks/lifecycle/launch"
	"github.com/buildpacks/lifecycle/platform/files"
)

const (
	TargetLabel                = "io.buildpacks.id"
	OSDistributionNameLabel    = "io.buildpacks.distribution.name"
	OSDistributionVersionLabel = "io.buildpacks.distribution.version"
)

func GetRunImageForExport(inputs LifecycleInputs) (files.RunImageForExport, error) {
	if inputs.PlatformAPI.LessThan("0.12") {
		stackMD, err := files.ReadStack(inputs.StackPath, cmd.DefaultLogger)
		if err != nil {
			return files.RunImageForExport{}, err
		}
		return stackMD.RunImage, nil
	}
	runMD, err := files.ReadRun(inputs.RunPath, cmd.DefaultLogger)
	if err != nil {
		return files.RunImageForExport{}, err
	}
	if len(runMD.Images) == 0 {
		return files.RunImageForExport{}, nil
	}
	inputRef := iname.ParseMaybe(inputs.RunImageRef)
	for _, runImage := range runMD.Images {
		if iname.ParseMaybe(runImage.Image) == inputRef {
			return runImage, nil
		}
		for _, mirror := range runImage.Mirrors {
			if iname.ParseMaybe(mirror) == inputRef {
				return runImage, nil
			}
		}
	}
	buildMD := &files.BuildMetadata{}
	if err = files.DecodeBuildMetadata(launch.GetMetadataFilePath(inputs.LayersDir), inputs.PlatformAPI, buildMD); err != nil {
		return files.RunImageForExport{}, err
	}
	if len(buildMD.Extensions) > 0 {
		// Extensions could have switched the run image, so we can't assume the first run image in run.toml was intended
		return files.RunImageForExport{Image: inputs.RunImageRef}, nil
	}
	return runMD.Images[0], nil
}

func BestRunImageMirrorFor(targetRegistry string, runImageMD files.RunImageForExport, checkReadAccess CheckReadAccess) (string, error) {
	var runImageMirrors []string
	if runImageMD.Image == "" {
		return "", errors.New("missing run image metadata")
	}
	runImageMirrors = append(runImageMirrors, runImageMD.Image)
	runImageMirrors = append(runImageMirrors, runImageMD.Mirrors...)

	keychain, err := auth.DefaultKeychain(runImageMirrors...)
	if err != nil {
		return "", fmt.Errorf("unable to create keychain: %w", err)
	}

	// Try to select run image on the same registry as the target
	runImageRef := byRegistry(targetRegistry, runImageMirrors, checkReadAccess, keychain)
	if runImageRef != "" {
		return runImageRef, nil
	}

	// Select the first run image we have access to
	for _, image := range runImageMirrors {
		if ok, _ := checkReadAccess(image, keychain); ok {
			return image, nil
		}
	}

	return "", errors.New("failed to find accessible run image")
}

func byRegistry(reg string, images []string, checkReadAccess CheckReadAccess, keychain authn.Keychain) string {
	for _, image := range images {
		ref, err := name.ParseReference(image, name.WeakValidation)
		if err != nil {
			continue
		}
		if reg == ref.Context().RegistryStr() {
			if ok, _ := checkReadAccess(image, keychain); ok {
				return image
			}
		}
	}
	return ""
}
