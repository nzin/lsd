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

		// macOS
		{"macOS data volume is the one that matters", "apfs", "/System/Volumes/Data", true, true},
		{"macOS sealed system volume", "apfs", "/", true, false},
		{"macOS VM volume", "apfs", "/System/Volumes/VM", true, false},
		{"macOS Preboot volume", "apfs", "/System/Volumes/Preboot", true, false},
		{"macOS Update volume", "apfs", "/System/Volumes/Update", true, false},
		{"macOS iSCPreboot volume", "apfs", "/System/Volumes/iSCPreboot", true, false},
		{"macOS recovery volume", "apfs", "/Volumes/Recovery", true, false},
		{"macOS Time Machine local snapshot", "apfs", "/Volumes/.timemachine/ABC/2026-01-01-000000.local", true, false},
		{"macOS mounted snapshot", "apfs", "/Volumes/com.apple.TimeMachine.localsnapshots/Backups.backupdb", true, false},
		{"external APFS drive", "apfs", "/Volumes/Backup", true, true},
		{"external HFS+ drive", "hfs", "/Volumes/TimeMachine", true, true},
		{"old macOS with an HFS+ root", "hfs", "/", true, true},
		{"FAT usb stick on macOS", "msdos", "/Volumes/USB", true, true},
		{"exFAT usb stick", "exfat", "/Volumes/Stick", true, true},
		{"macOS autofs home", "autofs", "/System/Volumes/Data/home", true, false},
		{"macOS smb share stays out", "smbfs", "/Volumes/share", true, false},
		{"a volume named like the prefix is not skipped", "apfs", "/Volumes/Recovery-Drive", true, true},
	} {
		if got := wantDisk(c.fstype, c.mount, c.isDir); got != c.want {
			t.Errorf("%s: wantDisk(%q, %q, dir=%v) = %v, want %v", c.name, c.fstype, c.mount, c.isDir, got, c.want)
		}
	}
}
