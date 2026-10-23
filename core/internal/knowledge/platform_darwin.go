//go:build darwin

package knowledge

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// seekdb's macOS runtime needs macOS 15.6.1 or later.
var minimumMacOS = [3]int{15, 6, 1}

// platformReason names why this Mac cannot run seekdb, or "" when it can.
func platformReason() string {
	product, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	if !atLeast(parseVersion(product), minimumMacOS) {
		return "macos_version"
	}
	return ""
}

func parseVersion(value string) [3]int {
	var version [3]int
	for index, part := range strings.SplitN(strings.TrimSpace(value), ".", 3) {
		number, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		version[index] = number
	}
	return version
}

func atLeast(version, minimum [3]int) bool {
	for index := range version {
		if version[index] != minimum[index] {
			return version[index] > minimum[index]
		}
	}
	return true
}
