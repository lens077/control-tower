package deploy_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type topologySpreadConstraint struct {
	MaxSkew            int    `yaml:"maxSkew"`
	TopologyKey        string `yaml:"topologyKey"`
	WhenUnsatisfiable  string `yaml:"whenUnsatisfiable"`
	NodeAffinityPolicy string `yaml:"nodeAffinityPolicy"`
	NodeTaintsPolicy   string `yaml:"nodeTaintsPolicy"`
	LabelSelector      struct {
		MatchLabels map[string]string `yaml:"matchLabels"`
	} `yaml:"labelSelector"`
}

type rollingStrategy struct {
	Type          string `yaml:"type"`
	RollingUpdate struct {
		MaxUnavailable int `yaml:"maxUnavailable"`
		MaxSurge       int `yaml:"maxSurge"`
	} `yaml:"rollingUpdate"`
}

type deployment struct {
	Spec struct {
		Strategy rollingStrategy `yaml:"strategy"`
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"selector"`
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
			Spec struct {
				Affinity struct {
					PodAntiAffinity struct {
						Required []struct {
							TopologyKey   string `yaml:"topologyKey"`
							LabelSelector struct {
								MatchLabels map[string]string `yaml:"matchLabels"`
							} `yaml:"labelSelector"`
						} `yaml:"requiredDuringSchedulingIgnoredDuringExecution"`
					} `yaml:"podAntiAffinity"`
				} `yaml:"affinity"`
				TopologySpreadConstraints []topologySpreadConstraint `yaml:"topologySpreadConstraints"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func TestGatewayDeploymentSpreadsAcrossNodes(t *testing.T) {
	for _, environment := range []string{"dev", "pre"} {
		environment := environment
		t.Run(environment, func(t *testing.T) {
			path := filepath.Join(environment, "gateway", "deployment.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			var manifest deployment
			if err := yaml.Unmarshal(data, &manifest); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}

			const partOf = "app.kubernetes.io/part-of"
			labels := manifest.Spec.Template.Metadata.Labels
			if labels[partOf] != "ecommerce" {
				t.Errorf("%s pod template must label %s=ecommerce", path, partOf)
			}
			if _, ok := manifest.Spec.Selector.MatchLabels[partOf]; ok {
				t.Errorf("%s must not add %s to the immutable Deployment selector", path, partOf)
			}
			strategy := manifest.Spec.Strategy
			if strategy.Type != "RollingUpdate" || strategy.RollingUpdate.MaxUnavailable != 1 ||
				strategy.RollingUpdate.MaxSurge != 0 {
				t.Errorf("%s hard-spread gateway must use maxUnavailable=1 and maxSurge=0", path)
			}

			var strictApp, suiteWide bool
			for _, term := range manifest.Spec.Template.Spec.Affinity.PodAntiAffinity.Required {
				if term.TopologyKey == "kubernetes.io/hostname" &&
					term.LabelSelector.MatchLabels["app"] == "control-tower-gateway" {
					strictApp = true
				}
			}
			for _, spread := range manifest.Spec.Template.Spec.TopologySpreadConstraints {
				if spread.MaxSkew == 1 && spread.TopologyKey == "kubernetes.io/hostname" &&
					spread.WhenUnsatisfiable == "DoNotSchedule" &&
					spread.NodeAffinityPolicy == "Honor" && spread.NodeTaintsPolicy == "Honor" &&
					spread.LabelSelector.MatchLabels[partOf] == "ecommerce" {
					suiteWide = true
				}
			}
			if !strictApp || !suiteWide {
				t.Errorf("%s must contain gateway pod anti-affinity and suite-wide node spread", path)
			}
		})
	}
}
