package quick

import (
	"context"
	"strings"
	"testing"

	spotcontrol "github.com/mcMineyC/spotcontrol"
)

func TestApplyDefaults_DeviceName(t *testing.T) {
	cfg := ApplyDefaults(QuickConfig{})
	if cfg.DeviceName != "SpotControl" {
		t.Errorf("expected default DeviceName %q, got %q", "SpotControl", cfg.DeviceName)
	}
}

func TestApplyDefaults_DeviceNameCustom(t *testing.T) {
	cfg := ApplyDefaults(QuickConfig{DeviceName: "MyDevice"})
	if cfg.DeviceName != "MyDevice" {
		t.Errorf("expected DeviceName %q, got %q", "MyDevice", cfg.DeviceName)
	}
}

func TestApplyDefaults_DeviceType(t *testing.T) {
	cfg := ApplyDefaults(QuickConfig{})
	if cfg.DeviceType != spotcontrol.DeviceTypeComputer {
		t.Errorf("expected default DeviceType %v, got %v", spotcontrol.DeviceTypeComputer, cfg.DeviceType)
	}
}

func TestApplyDefaults_DeviceTypeCustom(t *testing.T) {
	cfg := ApplyDefaults(QuickConfig{DeviceType: spotcontrol.DeviceTypeSpeaker})
	if cfg.DeviceType != spotcontrol.DeviceTypeSpeaker {
		t.Errorf("expected DeviceType %v, got %v", spotcontrol.DeviceTypeSpeaker, cfg.DeviceType)
	}
}

func TestApplyDefaults_LoggerNotNil(t *testing.T) {
	cfg := ApplyDefaults(QuickConfig{})
	if cfg.Log == nil {
		t.Error("expected default Log to be non-nil")
	}
}

func TestApplyDefaults_LoggerPreserved(t *testing.T) {
	custom := spotcontrol.NewSimpleLogger(nil)
	cfg := ApplyDefaults(QuickConfig{Log: custom})
	if cfg.Log != custom {
		t.Error("expected custom Log to be preserved")
	}
}

func TestApplyDefaults_PreservesOtherFields(t *testing.T) {
	cfg := ApplyDefaults(QuickConfig{
		StatePath:    "/tmp/test.json",
		DeviceId:     "abcdef0123456789abcdef0123456789abcdef01",
		Interactive:  true,
		CallbackPort: 8080,
	})

	if cfg.StatePath != "/tmp/test.json" {
		t.Errorf("StatePath not preserved: got %q", cfg.StatePath)
	}
	if cfg.DeviceId != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("DeviceId not preserved: got %q", cfg.DeviceId)
	}
	if !cfg.Interactive {
		t.Error("Interactive not preserved")
	}
	if cfg.CallbackPort != 8080 {
		t.Errorf("CallbackPort not preserved: got %d", cfg.CallbackPort)
	}
}

func TestConnect_NoCredentialsError(t *testing.T) {
	// Connect with no state file and Interactive=false should fail with a
	// clear error about missing credentials.
	ctx := context.Background()
	_, err := Connect(ctx, QuickConfig{
		Interactive: false,
		// No StatePath, no stored credentials.
	})
	if err == nil {
		t.Fatal("expected error when no credentials are available")
	}

	// The error should mention credentials.
	if !strings.Contains(err.Error(), "no credentials available") {
		t.Errorf("expected error to contain %q, got: %q", "no credentials available", err.Error())
	}
}

func TestConnect_MissingStateFileNotError(t *testing.T) {
	// If StatePath points to a nonexistent file, Connect should not fail on
	// loading state — it should treat it as "no prior state" and then fail
	// only because there are no credentials (when Interactive is false).
	ctx := context.Background()
	_, err := Connect(ctx, QuickConfig{
		StatePath:   "/tmp/spotcontrol_test_nonexistent_state_98765.json",
		Interactive: false,
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// Should fail on credentials, not on file loading.
	if strings.Contains(err.Error(), "failed loading state") {
		t.Errorf("expected missing state file to be silently ignored, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no credentials available") {
		t.Errorf("expected credentials error, got: %v", err)
	}
}
