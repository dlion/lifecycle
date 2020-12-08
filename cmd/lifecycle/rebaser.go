package main

import (
	"fmt"

	"github.com/buildpacks/imgutil"
	"github.com/buildpacks/imgutil/local"
	"github.com/buildpacks/imgutil/remote"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"

	"github.com/buildpacks/lifecycle"
	"github.com/buildpacks/lifecycle/auth"
	"github.com/buildpacks/lifecycle/cmd"
	"github.com/buildpacks/lifecycle/image"
	"github.com/buildpacks/lifecycle/priv"
)

type rebaseCmd struct {
	//flags: inputs
	imageNames            []string
	reportPath            string
	runImageRef           string
	deprecatedRunImageRef string
	platformAPI           string
	useDaemon             bool
	uid, gid              int

	//set if necessary before dropping privileges
	docker   client.CommonAPIClient
	keychain authn.Keychain
}

func (r *rebaseCmd) DefineFlags() {
	cmd.FlagGID(&r.gid)
	cmd.FlagReportPath(&r.reportPath)
	cmd.FlagRunImage(&r.runImageRef)
	cmd.FlagUID(&r.uid)
	cmd.FlagUseDaemon(&r.useDaemon)

	cmd.DeprecatedFlagRunImage(&r.deprecatedRunImageRef)
}

func (r *rebaseCmd) Args(nargs int, args []string) error {
	if nargs == 0 {
		return cmd.FailErrCode(errors.New("at least one image argument is required"), cmd.CodeInvalidArgs, "parse arguments")
	}
	r.imageNames = args
	if err := image.ValidateDestinationTags(r.useDaemon, r.imageNames...); err != nil {
		return cmd.FailErrCode(err, cmd.CodeInvalidArgs, "validate image tag(s)")
	}

	if r.deprecatedRunImageRef != "" && r.runImageRef != "" {
		return cmd.FailErrCode(errors.New("supply only one of -run-image or (deprecated) -image"), cmd.CodeInvalidArgs, "parse arguments")
	}
	if r.deprecatedRunImageRef != "" {
		r.runImageRef = r.deprecatedRunImageRef
	}

	if r.reportPath == cmd.PlaceholderReportPath {
		r.reportPath = cmd.DefaultReportPath(r.platformAPI, "")
	}

	return nil
}

func (r *rebaseCmd) Privileges() error {
	keychain, err := r.resolveKeychain()
	if err != nil {
		return cmd.FailErr(err, "resolve keychain")
	}
	r.keychain = keychain

	if r.useDaemon {
		var err error
		r.docker, err = priv.DockerClient()
		if err != nil {
			return cmd.FailErr(err, "initialize docker client")
		}
	}
	if err := priv.RunAs(r.uid, r.gid); err != nil {
		return cmd.FailErr(err, fmt.Sprintf("exec as user %d:%d", r.uid, r.gid))
	}
	return nil
}

func (r *rebaseCmd) Exec() error {
	ref, err := name.ParseReference(r.imageNames[0], name.WeakValidation)
	if err != nil {
		return err
	}
	registry := ref.Context().RegistryStr()

	var appImage imgutil.Image
	if r.useDaemon {
		appImage, err = local.NewImage(
			r.imageNames[0],
			r.docker,
			local.FromBaseImage(r.imageNames[0]),
		)
	} else {
		appImage, err = remote.NewImage(
			r.imageNames[0],
			r.keychain,
			remote.FromBaseImage(r.imageNames[0]),
		)
	}
	if err != nil || !appImage.Found() {
		return cmd.FailErr(err, "access image to rebase")
	}

	var md lifecycle.LayersMetadata
	if err := lifecycle.DecodeLabel(appImage, lifecycle.LayerMetadataLabel, &md); err != nil {
		return err
	}

	if r.runImageRef == "" {
		if md.Stack.RunImage.Image == "" {
			return cmd.FailErrCode(errors.New("-image is required when there is no stack metadata available"), cmd.CodeInvalidArgs, "parse arguments")
		}
		r.runImageRef, err = md.Stack.BestRunImageMirror(registry)
		if err != nil {
			return err
		}
	}

	var newBaseImage imgutil.Image
	if r.useDaemon {
		newBaseImage, err = local.NewImage(
			r.imageNames[0],
			r.docker,
			local.FromBaseImage(r.runImageRef),
		)
	} else {
		newBaseImage, err = remote.NewImage(
			r.imageNames[0],
			r.keychain,
			remote.FromBaseImage(r.runImageRef),
		)
	}
	if err != nil || !newBaseImage.Found() {
		return cmd.FailErr(err, "access run image")
	}

	rebaser := &lifecycle.Rebaser{
		Logger: cmd.DefaultLogger,
	}
	report, err := rebaser.Rebase(appImage, newBaseImage, r.imageNames[1:])
	if err != nil {
		return cmd.FailErrCode(err, cmd.CodeRebaseError, "rebase")
	}
	if err := lifecycle.WriteTOML(r.reportPath, &report); err != nil {
		return cmd.FailErrCode(err, cmd.CodeRebaseError, "write rebase report")
	}
	return nil
}

func (r *rebaseCmd) resolveKeychain() (authn.Keychain, error) {
	return auth.ResolveKeychain(cmd.EnvRegistryAuth, r.imageNames)
}
