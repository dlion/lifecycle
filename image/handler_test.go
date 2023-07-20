package image

import (
	"testing"

	"github.com/buildpacks/pack/pkg/testmocks"
	"github.com/golang/mock/gomock"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"

	h "github.com/buildpacks/lifecycle/testhelpers"
	testmockauth "github.com/buildpacks/lifecycle/testmock/auth"
)

//go:generate mockgen -package testmockauth -destination ../testmock/auth/mock_keychain.go github.com/google/go-containerregistry/pkg/authn Keychain

func TestHandler(t *testing.T) {
	spec.Run(t, "VerifyAPIs", testHandler, spec.Sequential(), spec.Report(report.Terminal{}))
}

func testHandler(t *testing.T, when spec.G, it spec.S) {
	var (
		mockController *gomock.Controller
		mockKeychain   *testmockauth.MockKeychain
	)

	it.Before(func() {
		mockController = gomock.NewController(t)
		mockKeychain = testmockauth.NewMockKeychain(mockController)
	})

	when("Remote handler", func() {
		it("returns a remote handler", func() {
			handler := NewHandler(nil, mockKeychain, "", false)

			_, ok := handler.(*RemoteHandler)

			h.AssertEq(t, ok, true)
		})
	})

	when("Local handler", func() {
		it("returns a local handler", func() {
			mockController := gomock.NewController(t)
			mockDockerClient := testmocks.NewMockCommonAPIClient(mockController)

			handler := NewHandler(mockDockerClient, mockKeychain, "", false)

			_, ok := handler.(*LocalHandler)

			h.AssertEq(t, ok, true)
		})
	})

	when("Layout handler", func() {
		it("returns a layout handler", func() {
			handler := NewHandler(nil, mockKeychain, "random-dir", true)

			_, ok := handler.(*LayoutHandler)

			h.AssertEq(t, ok, true)
		})
	})
}