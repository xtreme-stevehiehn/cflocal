package cmd_test

import (
	"io/ioutil"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/fatih/color"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	. "github.com/sclevine/cflocal/cf/cmd"
	"github.com/sclevine/cflocal/cf/cmd/mocks"
	sharedmocks "github.com/sclevine/cflocal/mocks"
	"github.com/sclevine/forge"
	"github.com/sclevine/forge/app"
	"github.com/sclevine/forge/engine"
)

var _ = Describe("Run", func() {
	var (
		mockCtrl      *gomock.Controller
		mockUI        *sharedmocks.MockUI
		mockStager    *mocks.MockStager
		mockRunner    *mocks.MockRunner
		mockForwarder *mocks.MockForwarder
		mockRemoteApp *mocks.MockRemoteApp
		mockFS        *mocks.MockFS
		mockHelp      *mocks.MockHelp
		mockConfig    *mocks.MockConfig
		cmd           *Run
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockUI = sharedmocks.NewMockUI()
		mockStager = mocks.NewMockStager(mockCtrl)
		mockRunner = mocks.NewMockRunner(mockCtrl)
		mockForwarder = mocks.NewMockForwarder(mockCtrl)
		mockRemoteApp = mocks.NewMockRemoteApp(mockCtrl)
		mockFS = mocks.NewMockFS(mockCtrl)
		mockHelp = mocks.NewMockHelp(mockCtrl)
		mockConfig = mocks.NewMockConfig(mockCtrl)
		cmd = &Run{
			UI:        mockUI,
			Stager:    mockStager,
			Runner:    mockRunner,
			Forwarder: mockForwarder,
			RemoteApp: mockRemoteApp,
			FS:        mockFS,
			Help:      mockHelp,
			Config:    mockConfig,
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Describe("#Match", func() {
		It("should return true when the first argument is run", func() {
			Expect(cmd.Match([]string{"run"})).To(BeTrue())
			Expect(cmd.Match([]string{"not-run"})).To(BeFalse())
			Expect(cmd.Match([]string{})).To(BeFalse())
			Expect(cmd.Match(nil)).To(BeFalse())
		})
	})

	Describe("#Run", func() {
		It("should run a droplet", func() {
			droplet := sharedmocks.NewMockBuffer("some-droplet")
			launcher := sharedmocks.NewMockBuffer("some-launcher")
			sshpass := sharedmocks.NewMockBuffer("some-sshpass")
			services := forge.Services{"some": {{Name: "services"}}}
			forwardedServices := forge.Services{"some": {{Name: "forwarded-services"}}}
			restart := make(<-chan time.Time)
			watchDone := make(chan struct{})
			health := make(chan string, 3)
			forwardDone, forwardDoneCalls := sharedmocks.NewMockFunc()

			forwardConfig := &forge.ForwardDetails{
				Host: "some-ssh-host",
			}
			localYML := &app.LocalYML{
				Applications: []*forge.AppConfig{
					{Name: "some-other-app"},
					{
						Name:     "some-app",
						Env:      map[string]string{"a": "b"},
						Services: forge.Services{"some": {{Name: "overwritten-services"}}},
					},
				},
			}
			mockFS.EXPECT().Abs("some-dir").Return("some-abs-dir", nil)
			mockConfig.EXPECT().Load().Return(localYML, nil)
			mockFS.EXPECT().ReadFile("./some-app.droplet").Return(droplet, int64(100), nil)
			mockStager.EXPECT().Download("/tmp/lifecycle/launcher", LatestStack).Return(engine.NewStream(launcher, 200), nil)
			mockRemoteApp.EXPECT().Services("some-service-app").Return(services, nil)
			mockRemoteApp.EXPECT().Forward("some-forward-app", services).Return(forwardedServices, forwardConfig, nil)
			mockStager.EXPECT().Download("/usr/bin/sshpass", LatestStack).Return(engine.NewStream(sshpass, 300), nil)

			gomock.InOrder(
				mockFS.EXPECT().MakeDirAll("some-abs-dir"),
				mockFS.EXPECT().Watch("some-abs-dir", time.Second).Return(restart, watchDone, nil),
				mockForwarder.EXPECT().Forward(gomock.Any()).Return(health, forwardDone, "some-container-id", nil).Do(
					func(config *forge.ForwardConfig) {
						Expect(ioutil.ReadAll(config.SSHPass)).To(Equal([]byte("some-sshpass")))
						Expect(config.AppName).To(Equal("some-app"))
						Expect(config.Stack).To(Equal(LatestStack))
						Expect(config.Color("some-text")).To(Equal(color.GreenString("some-text")))
						Expect(config.Details).To(Equal(forwardConfig))
						Expect(config.HostIP).To(Equal("0.0.0.0"))
						Expect(config.HostPort).To(Equal("3000"))
						Eventually(config.Wait).Should(Receive())
					},
				),
				mockRunner.EXPECT().Run(gomock.Any()).Return(int64(0), nil).Do(
					func(config *forge.RunConfig) {
						Expect(ioutil.ReadAll(config.Droplet)).To(Equal([]byte("some-droplet")))
						Expect(ioutil.ReadAll(config.Launcher)).To(Equal([]byte("some-launcher")))
						Expect(config.Stack).To(Equal(LatestStack))
						Expect(config.AppDir).To(Equal("some-abs-dir"))
						Expect(config.RSync).To(BeTrue())
						Expect(config.Restart).To(Equal(restart))
						Expect(config.Color("some-text")).To(Equal(color.GreenString("some-text")))
						Expect(config.AppConfig).To(Equal(&forge.AppConfig{
							Name:     "some-app",
							Env:      map[string]string{"a": "b"},
							Services: forwardedServices,
						}))
						Expect(config.NetworkConfig).To(Equal(&forge.NetworkConfig{
							ContainerID: "some-container-id",
							HostIP:      "0.0.0.0",
							HostPort:    "3000",
						}))
					},
				),
			)
			health <- types.Starting
			health <- types.Starting
			health <- types.Healthy
			Expect(cmd.Run([]string{
				"run", "some-app",
				"-i", "0.0.0.0",
				"-p", "3000",
				"-d", "some-dir", "-r", "-w",
				"-s", "some-service-app",
				"-f", "some-forward-app",
			})).To(Succeed())
			Expect(forwardDoneCalls()).To(Equal(1))
			Expect(droplet.Result()).To(BeEmpty())
			Expect(launcher.Result()).To(BeEmpty())
			Expect(sshpass.Result()).To(BeEmpty())
			Expect(watchDone).To(BeClosed())
			Expect(mockUI.Out).To(gbytes.Say("Running some-app on port 3000..."))
		})

		// TODO: test app dir when app dir is unspecified (currently tested by integration)
		// TODO: test without watching / without rsync
		// TODO: test -w / -r without -d
		// TODO: test free port picker when port is unspecified (currently tested by integration)
		// TODO: test different combinations of -s and -f
		// TODO: test without -f
		// TODO: test timeout
		// TODO: test wait interval + done
	})
})
