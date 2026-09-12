//go:build linux
// +build linux

package host

import (
	"github.com/opencontainers/cgroups/devices/config"
)

// DefaultCapabilities is the default list of capabilities which are set inside
// a container, taken from:
// https://github.com/opencontainers/runc/blob/v1.0.0-rc8/libcontainer/SPEC.md#security
var DefaultCapabilities = []string{
	"CAP_NET_RAW",
	"CAP_NET_BIND_SERVICE",
	"CAP_AUDIT_READ",
	"CAP_AUDIT_WRITE",
	"CAP_DAC_OVERRIDE",
	"CAP_SETFCAP",
	"CAP_SETPCAP",
	"CAP_SETGID",
	"CAP_SETUID",
	"CAP_MKNOD",
	"CAP_CHOWN",
	"CAP_FOWNER",
	"CAP_FSETID",
	"CAP_KILL",
	"CAP_SYS_CHROOT",
}

// defaultSimpleDevices are devices that are to be both allowed and created
// inside containers. This is a copy of the default device list shipped with
// runc v1.0.0-rc8 (libcontainer/configs/device_defaults.go); later runc
// releases dropped the built-in defaults, so Flynn carries its own.
var defaultSimpleDevices = []*config.Device{
	// /dev/null and zero
	{
		Rule:     config.Rule{Type: config.CharDevice, Major: 1, Minor: 3, Permissions: "rwm", Allow: true},
		Path:     "/dev/null",
		FileMode: 0666,
	},
	{
		Rule:     config.Rule{Type: config.CharDevice, Major: 1, Minor: 5, Permissions: "rwm", Allow: true},
		Path:     "/dev/zero",
		FileMode: 0666,
	},
	{
		Rule:     config.Rule{Type: config.CharDevice, Major: 1, Minor: 7, Permissions: "rwm", Allow: true},
		Path:     "/dev/full",
		FileMode: 0666,
	},

	// consoles and ttys
	{
		Rule:     config.Rule{Type: config.CharDevice, Major: 5, Minor: 0, Permissions: "rwm", Allow: true},
		Path:     "/dev/tty",
		FileMode: 0666,
	},

	// /dev/urandom,/dev/random
	{
		Rule:     config.Rule{Type: config.CharDevice, Major: 1, Minor: 9, Permissions: "rwm", Allow: true},
		Path:     "/dev/urandom",
		FileMode: 0666,
	},
	{
		Rule:     config.Rule{Type: config.CharDevice, Major: 1, Minor: 8, Permissions: "rwm", Allow: true},
		Path:     "/dev/random",
		FileMode: 0666,
	},
}

// DefaultAllowedRules is the default list of cgroup device rules containers
// are allowed to access.
var DefaultAllowedRules = append([]*config.Rule{
	// allow mknod for any device
	{Type: config.CharDevice, Major: config.Wildcard, Minor: config.Wildcard, Permissions: "m", Allow: true},
	{Type: config.BlockDevice, Major: config.Wildcard, Minor: config.Wildcard, Permissions: "m", Allow: true},

	{Type: config.CharDevice, Major: 5, Minor: 1, Permissions: "rwm", Allow: true}, // /dev/console

	// /dev/pts/
	{Type: config.CharDevice, Major: 136, Minor: config.Wildcard, Permissions: "rwm", Allow: true},
	{Type: config.CharDevice, Major: 5, Minor: 2, Permissions: "rwm", Allow: true},

	// tuntap
	{Type: config.CharDevice, Major: 10, Minor: 200, Permissions: "rwm", Allow: true},
}, deviceRules(defaultSimpleDevices)...)

// DefaultAllowedDevices is the default list of devices containers are allowed
// to access, in the host wire format.
var DefaultAllowedDevices = fromConfigRules(DefaultAllowedRules)

// DefaultAutoCreatedDevices is the default list of devices created inside
// containers
var DefaultAutoCreatedDevices = fromConfigRules(DefaultAllowedRules)

func (d *Device) CgroupRule() *config.Rule {
	return &config.Rule{
		Type:        config.Type(d.Type),
		Major:       d.Major,
		Minor:       d.Minor,
		Permissions: config.Permissions(d.Permissions),
		Allow:       d.Allow,
	}
}

func (d *Device) Config() *config.Device {
	return &config.Device{
		Rule:     config.Rule{Type: config.Type(d.Type), Major: d.Major, Minor: d.Minor, Permissions: config.Permissions(d.Permissions), Allow: d.Allow},
		Path:     d.Path,
		FileMode: d.FileMode,
		Uid:      d.Uid,
		Gid:      d.Gid,
	}
}

// deviceRules extracts the cgroup device rules from auto-created devices.
func deviceRules(ds []*config.Device) []*config.Rule {
	res := make([]*config.Rule, len(ds))
	for i, d := range ds {
		r := d.Rule
		res[i] = &r
	}
	return res
}

// fromConfigRules converts cgroup device rules to the host wire format.
func fromConfigRules(rs []*config.Rule) []*Device {
	res := make([]*Device, len(rs))
	for i, r := range rs {
		res[i] = &Device{
			Type:        rune(r.Type),
			Major:       r.Major,
			Minor:       r.Minor,
			Permissions: string(r.Permissions),
			Allow:       r.Allow,
		}
	}
	return res
}

func CgroupRules(ds []*Device) []*config.Rule {
	res := make([]*config.Rule, len(ds))
	for i, d := range ds {
		res[i] = d.CgroupRule()
	}
	return res
}

// ConfigDevices converts devices to the libcontainer auto-created device
// format.
func ConfigDevices(ds []*Device) []*config.Device {
	res := make([]*config.Device, len(ds))
	for i, d := range ds {
		res[i] = d.Config()
	}
	return res
}
