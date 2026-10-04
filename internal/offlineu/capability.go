package offlineu

import (
	"os"
	"strconv"
	"strings"
)

// capDACOverride is CAP_DAC_OVERRIDE (bit 1 of the capability sets in
// /proc/self/status): the capability that lets a process bypass the read, write
// and execute permission bits of every file it can reach.
//
// The container image uses it so that the unprivileged user (uid 10001) can
// still read a course folder that belongs to root or to another NAS account:
// the same binary is shipped a second time as /app/offlineu-cap with that file
// capability, and /entrypoint.sh starts it whenever Docker allows the exec.
// Nothing has to be chmod-ed on the NAS and "使用高权限执行容器" (--privileged) does
// not have to be ticked - privilege is bound to the user a container runs as, and
// the kernel drops the capabilities when a non-root process executes a file that
// carries none.
//
// The capability is bounded by the container's capability bounding set, so
// --cap-drop DAC_OVERRIDE, --cap-drop ALL and no-new-privileges take it away
// again. That is exactly what the checks below report.
const capDACOverride = 1

// ReadCapability reports whether this process may bypass file permission checks,
// and whether that could be determined at all. known is false where
// /proc/self/status does not exist (Windows, macOS): nothing may be concluded
// there, so the UI stays quiet instead of blaming Docker for it.
func ReadCapability() (readable bool, known bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, found := strings.CutPrefix(line, "CapEff:")
		if !found {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return false, false
		}
		return mask&(1<<capDACOverride) != 0, true
	}
	return false, false
}

// RunAsUser names the user the process runs as, so a message about a denied
// folder can point at it. Inside a container this is normally "uid 10001", but
// the container may also run as the folder's owner ("1000:1000") or as root.
func RunAsUser() string {
	if uid := os.Getuid(); uid >= 0 {
		return "uid " + strconv.Itoa(uid)
	}
	return "this user"
}

// unreadableSummary explains a mapped folder the process may not open. With the
// read capability in place a plain permission problem cannot happen, so the
// wording names what is really missing: the capability, or the consent of a
// network share / ACL that is evaluated somewhere else.
func unreadableSummary(err string) string {
	switch readable, known := ReadCapability(); {
	case !known:
		return "not readable (" + RunAsUser() + ")" + errorSuffix(err)
	case readable:
		return "not readable even with CAP_DAC_OVERRIDE (" + RunAsUser() + ")" + errorSuffix(err)
	default:
		return "not readable (" + RunAsUser() + " without CAP_DAC_OVERRIDE)" + errorSuffix(err)
	}
}
