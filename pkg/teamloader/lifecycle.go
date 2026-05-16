package teamloader

import (
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/tools/lifecycle"
)

func lifecyclePolicyFromConfig(name string, cfg *latest.LifecycleConfig) lifecycle.Policy {
	return lifecycle.PolicyFromConfig(name, cfg)
}
