package image

import (
	"strings"

	"github.com/buildpacks/imgutil/remote"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/pkg/errors"

	"github.com/buildpacks/lifecycle/cmd"
)

// RegistryHandler takes care of the registry settings and checks
//
//go:generate mockgen -package testmock -destination testmock/registry_handler.go github.com/buildpacks/lifecycle RegistryHandler
type RegistryHandler interface {
	EnsureReadAccess(imageRefs ...string) error
	EnsureWriteAccess(imageRefs ...string) error
}

// DefaultRegistryHandler is the struct that implements the RegistryHandler methods
type DefaultRegistryHandler struct {
	keychain         authn.Keychain
	insecureRegistry []string
}

// NewRegistryHandler creates a new DefaultRegistryHandler
func NewRegistryHandler(keychain authn.Keychain, insecureRegistries []string) *DefaultRegistryHandler {
	return &DefaultRegistryHandler{
		keychain:         keychain,
		insecureRegistry: insecureRegistries,
	}
}

// EnsureReadAccess ensures that we can read from the registry
func (rv *DefaultRegistryHandler) EnsureReadAccess(imageRefs ...string) error {
	for _, imageRef := range imageRefs {
		if err := verifyReadAccess(imageRef, rv.keychain, GetInsecureOptions(rv.insecureRegistry, imageRef)); err != nil {
			return err
		}
	}
	return nil
}

// EnsureWriteAccess ensures that we can write to the registry
func (rv *DefaultRegistryHandler) EnsureWriteAccess(imageRefs ...string) error {
	for _, imageRef := range imageRefs {
		if err := verifyReadWriteAccess(imageRef, rv.keychain, GetInsecureOptions(rv.insecureRegistry, imageRef)); err != nil {
			return err
		}
	}
	return nil
}

// GetInsecureOptions returns a list of WithRegistrySetting imageOptions matching the specified imageRef prefix
/*
TODO: This is a temporary solution in order to get insecure registries in other components too
TODO: Ideally we should fix the `imgutil.options` struct visibility in order to mock and test the `remote.WithRegistrySetting`
TODO: function correctly and use the RegistryHandler everywhere it is needed.
*/
func GetInsecureOptions(insecureRegistries []string, imageRef string) []remote.ImageOption {
	var opts []remote.ImageOption
	if len(insecureRegistries) > 0 {
		for _, insecureRegistry := range insecureRegistries {
			if strings.HasPrefix(imageRef, insecureRegistry) {
				opts = append(opts, remote.WithRegistrySetting(insecureRegistry, true))
			}
		}
	}
	return opts
}

func verifyReadAccess(imageRef string, keychain authn.Keychain, opts []remote.ImageOption) error {
	if imageRef == "" {
		return nil
	}

	img, _ := remote.NewImage(imageRef, keychain, opts...)
	canRead, err := img.CheckReadAccess()
	if !canRead {
		cmd.DefaultLogger.Debugf("Error checking read access: %s", err)
		return errors.Errorf("ensure registry read access to %s", imageRef)
	}
	return nil
}

func verifyReadWriteAccess(imageRef string, keychain authn.Keychain, opts []remote.ImageOption) error {
	if imageRef == "" {
		return nil
	}

	img, _ := remote.NewImage(imageRef, keychain, opts...)
	canReadWrite, err := img.CheckReadWriteAccess()
	if !canReadWrite {
		cmd.DefaultLogger.Debugf("Error checking read/write access: %s", err)
		return errors.Errorf("ensure registry read/write access to %s", imageRef)
	}
	return nil
}
