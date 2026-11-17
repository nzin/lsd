package hostmetrics

import "testing"

func TestWantDisk(t *testing.T) {
	for _, c := range []struct {
		name          string
		fstype, mount string
		isDir, want   bool
	}{
		{"container root overlay", "overlay", "/", true, true},
		{"overlay mount of another container", "overlay", "/var/lib/docker/overlay2/abc/merged", true, false},
		{"host root", "ext4", "/", true, true},
		{"separate data disk", "xfs", "/data", true, true},
		{"efi partition", "vfat", "/boot/efi", true, true},
		{"docker desktop host share", "virtiofs", "/w", true, true},
		{"single-file bind mount", "ext4", "/etc/hosts", false, false},
		{"tmpfs", "tmpfs", "/dev/shm", true, false},
		{"proc", "proc", "/proc", true, false},
		{"sysfs", "sysfs", "/sys", true, false},
		{"cgroup2", "cgroup2", "/sys/fs/cgroup", true, false},
		{"network fs is excluded", "nfs4", "/mnt/nfs", true, false},
		{"snap squashfs", "squashfs", "/snap/core/1", true, false},
		{"real fs under /run", "ext4", "/run/media/usb", true, false},
		{"/runner is not /run", "ext4", "/runner", true, true},
		{"docker data dir", "ext4", "/var/lib/docker/volumes/x", true, false},
	} {
		if got := wantDisk(c.fstype, c.mount, c.isDir); got != c.want {
			t.Errorf("%s: wantDisk(%q, %q, dir=%v) = %v, want %v", c.name, c.fstype, c.mount, c.isDir, got, c.want)
		}
	}
}
