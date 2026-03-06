package spotcontrol

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// SpotifyVersionCode is the version code sent during AP key exchange.
const SpotifyVersionCode = 127700358

// version is set at build time via -ldflags.
var version string

// commitHash extracts the commit hash stored in the binary, if available.
func commitHash() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return ""
}

// VersionNumberString returns the version number as a string.
// If set via ldflags, it returns that value (without leading "v").
// Otherwise, it returns the first 8 characters of the commit hash, or "dev".
func VersionNumberString() string {
	if len(version) > 0 {
		return strings.TrimPrefix(version, "v")
	} else if commit := commitHash(); len(commit) >= 8 {
		return commit[:8]
	}
	return "dev"
}

// SpotifyLikeClientVersion returns a version string formatted like official Spotify clients.
func SpotifyLikeClientVersion() string {
	if len(version) > 0 {
		if commit := commitHash(); len(commit) >= 8 {
			return fmt.Sprintf("%s.g%s", version, commit[:8])
		}
		return version
	}
	return "0.0.0"
}

// VersionString returns the full version string including the project name.
func VersionString() string {
	return fmt.Sprintf("spotcontrol %s", VersionNumberString())
}

// SystemInfoString returns a system information string for use in protobuf fields.
func SystemInfoString() string {
	return fmt.Sprintf("%s; Go %s (%s %s)", VersionString(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// UserAgent returns the HTTP User-Agent string for spotcontrol requests.
func UserAgent() string {
	return fmt.Sprintf("spotcontrol/%s Go/%s", VersionNumberString(), runtime.Version())
}
