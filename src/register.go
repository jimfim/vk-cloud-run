package main

import (
	"kubelet-cloud-run/internal/provider"
	cloudrunprovider "kubelet-cloud-run/internal/provider/cloudrun"
	"kubelet-cloud-run/internal/provider/mock"
)

func registerMock(s *provider.Store) {
	/* #nosec */
	s.Register("mock", func(cfg provider.InitConfig) (provider.Provider, error) { //nolint:errcheck
		return mock.NewMockProvider(
			cfg.ConfigPath,
			cfg.NodeName,
			cfg.OperatingSystem,
			cfg.InternalIP,
			cfg.DaemonPort,
		)
	})
}

func registerCloudRun(s *provider.Store) {
	/* #nosec */
	s.Register("cloudrun", func(cfg provider.InitConfig) (provider.Provider, error) { //nolint:errcheck
		return cloudrunprovider.NewCloudRunProvider(
			cfg.ConfigPath,
			cfg.NodeName,
			cfg.OperatingSystem,
			cfg.InternalIP,
			cfg.DaemonPort,
		)
	})
}
