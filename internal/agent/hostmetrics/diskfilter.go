package hostmetrics

import "strings"

// Which mounts count as "disks". gopsutil's Partitions(false) ("physical only") returns nothing inside
// containers (the /dev/* device nodes do not exist there), so we list everything and filter ourselves.
//
// An allowlist of real, local filesystems: pseudo filesystems add noise, and network ones (nfs, cifs)
// are left out on purpose because statfs() on an unreachable server can hang the whole collector.
var diskFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true,
	"jfs": true, "reiserfs": true, "vfat": true, "exfat": true, "ntfs": true, "ntfs3": true,
	"fuseblk":  true, // ntfs-3g and friends
	"virtiofs": true, // host shares inside VMs (e.g. Docker Desktop)
	// macOS (development machines): APFS, HFS+, FAT. Network filesystems (smbfs, nfs, afpfs) stay out.
	"apfs": true, "hfs": true, "msdos": true,
}

// Mount points that are container / runtime plumbing rather than data disks.
var skipMountPrefixes = []string{
	"/proc", "/sys", "/dev", "/run",
	"/var/lib/docker/", "/var/lib/containers/", "/var/lib/kubelet/", "/snap/",
	// macOS: the recovery volume, and the Time Machine local snapshots mounted under /Volumes
	"/Volumes/Recovery", "/Volumes/.timemachine", "/Volumes/com.apple.TimeMachine.localsnapshots",
}

// wantDisk decides whether a mount should be reported. isDir is whether the mount point is a directory
// (single-file bind mounts such as /etc/hosts inside containers are not disks).
func wantDisk(fstype, mountpoint string, isDir bool) bool {
	if fstype == "overlay" {
		// A container's root filesystem is an overlay; every other overlay mount on a host
		// (one per running container) is noise.
		return mountpoint == "/"
	}
	if !diskFS[fstype] || !isDir {
		return false
	}
	// macOS (APFS, since Catalina): "/" is the sealed, read-only system volume (a few GB, misleading) and the
	// user's data lives in /System/Volumes/Data, which is the one whose fill level matters. The other volumes
	// below /System/Volumes (VM, Preboot, Update, ...) are system plumbing.
	if fstype == "apfs" && mountpoint == "/" {
		return false
	}
	if mountpoint == "/System/Volumes/Data" {
		return true
	}
	if strings.HasPrefix(mountpoint, "/System/Volumes/") {
		return false
	}
	for _, p := range skipMountPrefixes {
		dir := strings.TrimSuffix(p, "/")
		if mountpoint == dir || strings.HasPrefix(mountpoint, dir+"/") { // path boundary: /run yes, /runner no
			return false
		}
	}
	return true
}
