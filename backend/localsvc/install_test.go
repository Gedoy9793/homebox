package localsvc

import (
	"os"
	"testing"

	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

func TestInstallLabelServiceSetsDefault(t *testing.T) {
	t.Setenv(EnvLabelServiceURL, "")

	installLabelService("http://127.0.0.1:9999")

	want := "http://127.0.0.1:9999" + LabelPath
	if got := os.Getenv(EnvLabelServiceURL); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// An operator who configured their own label service must keep it.
func TestInstallLabelServiceKeepsOperatorValue(t *testing.T) {
	t.Setenv(EnvLabelServiceURL, "https://labels.example.com/render")

	installLabelService("http://127.0.0.1:9999")

	if got := os.Getenv(EnvLabelServiceURL); got != "https://labels.example.com/render" {
		t.Fatalf("expected the configured service to be kept, got %q", got)
	}
}

// The same setting can be supplied as a command line flag, which conf reads with
// a higher priority than the environment — so it must not be overridden either.
func TestInstallLabelServiceRespectsCommandLineFlag(t *testing.T) {
	t.Setenv(EnvLabelServiceURL, "")

	original := os.Args
	t.Cleanup(func() { os.Args = original })

	for _, arg := range []string{
		"--label-maker-label-service-url=https://labels.example.com",
		"-label-maker-label-service-url=https://labels.example.com",
		"--label-maker-label-service-url",
	} {
		if err := os.Unsetenv(EnvLabelServiceURL); err != nil {
			t.Fatal(err)
		}

		os.Args = []string{"api", arg}
		installLabelService("http://127.0.0.1:9999")

		if got := os.Getenv(EnvLabelServiceURL); got != "" {
			t.Fatalf("expected %q to win, but the environment was set to %q", arg, got)
		}
	}
}

// The wiring hinges on this one variable name being the one Homebox reads, so it
// is checked against the configuration package rather than trusted.
func TestLabelServiceEnvironmentNameMatchesConfig(t *testing.T) {
	t.Setenv(EnvLabelServiceURL, "http://127.0.0.1:9999/label")

	original := os.Args
	os.Args = []string{"api"}
	t.Cleanup(func() { os.Args = original })

	cfg, err := config.New("test", "test")
	if err != nil {
		t.Skipf("configuration could not be parsed in this environment: %v", err)
	}

	if cfg.LabelMaker.LabelServiceUrl == nil {
		t.Fatalf("%s did not reach the configuration", EnvLabelServiceURL)
	}
	if *cfg.LabelMaker.LabelServiceUrl != "http://127.0.0.1:9999/label" {
		t.Fatalf("unexpected value %q", *cfg.LabelMaker.LabelServiceUrl)
	}
}
