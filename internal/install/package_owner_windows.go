//go:build windows

package install

func currentLinuxPackageMethod(string) Method { return MethodUnknown }

func TrustedSystemExecutable(string) bool { return false }
