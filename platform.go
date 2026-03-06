package spotcontrol

import (
	"runtime"

	spotifypb "github.com/mcMineyC/spotcontrol/proto/spotify"
	clienttokenpb "github.com/mcMineyC/spotcontrol/proto/spotify/clienttoken/data/v0"
)

// GetOS returns the protobuf Os enum corresponding to the current operating system.
func GetOS() spotifypb.Os {
	switch runtime.GOOS {
	case "android":
		return spotifypb.Os_OS_ANDROID
	case "darwin":
		return spotifypb.Os_OS_OSX
	case "freebsd":
		return spotifypb.Os_OS_FREEBSD
	case "ios":
		return spotifypb.Os_OS_IPHONE
	case "linux":
		return spotifypb.Os_OS_LINUX
	case "windows":
		return spotifypb.Os_OS_WINDOWS
	default:
		return spotifypb.Os_OS_UNKNOWN
	}
}

// GetCpuFamily returns the protobuf CpuFamily enum corresponding to the current CPU architecture.
func GetCpuFamily() spotifypb.CpuFamily {
	switch runtime.GOARCH {
	case "386":
		return spotifypb.CpuFamily_CPU_X86
	case "amd64":
		return spotifypb.CpuFamily_CPU_X86_64
	case "arm":
		return spotifypb.CpuFamily_CPU_ARM
	case "arm64":
		return spotifypb.CpuFamily_CPU_ARM
	case "mips", "mips64":
		return spotifypb.CpuFamily_CPU_MIPS
	case "ppc64":
		return spotifypb.CpuFamily_CPU_PPC_64
	default:
		return spotifypb.CpuFamily_CPU_UNKNOWN
	}
}

// GetPlatform returns the protobuf Platform enum corresponding to the current OS/arch combination.
func GetPlatform() spotifypb.Platform {
	switch runtime.GOOS {
	case "android":
		return spotifypb.Platform_PLATFORM_ANDROID_ARM
	case "darwin":
		switch runtime.GOARCH {
		case "386":
			return spotifypb.Platform_PLATFORM_OSX_X86
		case "amd64":
			return spotifypb.Platform_PLATFORM_OSX_X86_64
		case "arm64":
			return spotifypb.Platform_PLATFORM_OSX_X86_64
		case "ppc64":
			return spotifypb.Platform_PLATFORM_OSX_PPC
		}
	case "freebsd":
		switch runtime.GOARCH {
		case "386":
			return spotifypb.Platform_PLATFORM_FREEBSD_X86
		case "amd64":
			return spotifypb.Platform_PLATFORM_FREEBSD_X86_64
		}
	case "ios":
		switch runtime.GOARCH {
		case "arm":
			return spotifypb.Platform_PLATFORM_IPHONE_ARM
		case "arm64":
			return spotifypb.Platform_PLATFORM_IPHONE_ARM64
		}
	case "linux":
		switch runtime.GOARCH {
		case "386":
			return spotifypb.Platform_PLATFORM_LINUX_X86
		case "amd64":
			return spotifypb.Platform_PLATFORM_LINUX_X86_64
		case "mips", "mips64":
			return spotifypb.Platform_PLATFORM_LINUX_MIPS
		case "arm", "arm64":
			return spotifypb.Platform_PLATFORM_LINUX_ARM
		}
	case "windows":
		switch runtime.GOARCH {
		case "386":
			return spotifypb.Platform_PLATFORM_WIN32_X86
		case "amd64":
			return spotifypb.Platform_PLATFORM_WIN32_X86_64
		case "arm", "arm64":
			return spotifypb.Platform_PLATFORM_WINDOWS_CE_ARM
		}
	}

	return spotifypb.Platform_PLATFORM_GENERIC_PARTNER
}

// GetPlatformSpecificData returns the platform-specific data protobuf
// used when retrieving client tokens.
func GetPlatformSpecificData() *clienttokenpb.PlatformSpecificData {
	switch runtime.GOOS {
	case "android":
		return &clienttokenpb.PlatformSpecificData{
			Data: &clienttokenpb.PlatformSpecificData_Android{
				Android: &clienttokenpb.NativeAndroidData{},
			},
		}
	case "darwin":
		return &clienttokenpb.PlatformSpecificData{
			Data: &clienttokenpb.PlatformSpecificData_DesktopMacos{
				DesktopMacos: &clienttokenpb.NativeDesktopMacOSData{},
			},
		}
	case "ios":
		return &clienttokenpb.PlatformSpecificData{
			Data: &clienttokenpb.PlatformSpecificData_Ios{
				Ios: &clienttokenpb.NativeIOSData{},
			},
		}
	case "linux", "freebsd":
		return &clienttokenpb.PlatformSpecificData{
			Data: &clienttokenpb.PlatformSpecificData_DesktopLinux{
				DesktopLinux: &clienttokenpb.NativeDesktopLinuxData{},
			},
		}
	case "windows":
		return &clienttokenpb.PlatformSpecificData{
			Data: &clienttokenpb.PlatformSpecificData_DesktopWindows{
				DesktopWindows: &clienttokenpb.NativeDesktopWindowsData{},
			},
		}
	}

	return nil
}
