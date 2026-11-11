#!/usr/bin/env bash
# Run in a private mount namespace, with sudo unshare --mount --propagation private.
# Only newly allocated loop devices backed by files in our own temp directory.
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Requires root in a private mount namespace'; exit 77; }
[[ $(readlink /proc/self/ns/mnt) != $(readlink /proc/1/ns/mnt) ]] || { echo 'Refusing host mount namespace'; exit 1; }
for tool in btrfs mkfs.btrfs losetup mount sha256sum; do command -v "$tool" >/dev/null || exit 77; done
work=$(mktemp -d /tmp/nimos-btrfs-virtual.XXXXXX)
loops=()
cleanup() {
  set +e
  if mountpoint -q "$work/mnt"; then
    umount "$work/mnt" || { echo "Unmount failed; retaining images in $work"; return; }
  fi
  for dev in "${loops[@]}"; do losetup -d "$dev" || return; done
  rm -rf -- "$work"
}
trap cleanup EXIT
mkdir "$work/mnt"
new_disk() {
  local img="$work/$1.img" dev
  truncate -s 768M "$img"
  dev=$(losetup --find --show "$img")
  [[ $(losetup -n -O BACK-FILE "$dev") == "$img" ]] || exit 1
  loops+=("$dev")
  new_device=$dev
}
new_disk first; first=$new_device
new_disk second; second=$new_device
new_disk replacement; replacement=$new_device
mkfs.btrfs -q -d single -m dup "$first"
mount "$first" "$work/mnt"
dd if=/dev/urandom of="$work/mnt/payload" bs=1M count=32 status=none
(cd "$work/mnt" && sha256sum payload) > "$work/hashes"
verify() { (cd "$work/mnt" && sha256sum -c "$work/hashes"); }
btrfs subvolume create "$work/mnt/data" >/dev/null
cp "$work/mnt/payload" "$work/mnt/data/payload"
btrfs subvolume snapshot -r "$work/mnt/data" "$work/mnt/snapshot" >/dev/null
btrfs device add "$second" "$work/mnt"
btrfs balance start -dconvert=raid1 -mconvert=raid1 "$work/mnt"
verify
if [[ -n ${NIMOS_STORAGE_TEST_BINARY:-} ]]; then
  NIMOS_BTRFS_TEST_MOUNT="$work/mnt" "$NIMOS_STORAGE_TEST_BINARY" -test.run '^TestBalanceStatusVirtualBtrfs$' -test.v
fi
btrfs scrub start -B "$work/mnt"
umount "$work/mnt"
# Remove one virtual device, then recover with a fresh device.
losetup -d "$second"
loops=("$first" "$replacement")
btrfs device scan --forget "$second" || true
mount -o degraded "$first" "$work/mnt"
verify
missing_id=$(btrfs filesystem show "$work/mnt" | awk '/devid/ && /missing/ {print $2}')
[[ $missing_id =~ ^[0-9]+$ ]] || { echo 'Expected exactly one missing device'; exit 1; }
btrfs replace start -B "$missing_id" "$replacement" "$work/mnt"
btrfs balance start -dconvert=raid1,soft -mconvert=raid1,soft "$work/mnt"
btrfs scrub start -B "$work/mnt"
verify
cmp "$work/mnt/payload" "$work/mnt/snapshot/payload"
umount "$work/mnt"
mount "$first" "$work/mnt"
verify
# Exhaust capacity with bounded writes; the previously committed data must survive.
if dd if=/dev/zero of="$work/mnt/fill" bs=1M count=2048 conv=fsync 2>"$work/fill.log"; then
  echo 'Expected ENOSPC on the small virtual pool'; exit 1
fi
grep -q 'No space left on device' "$work/fill.log"
rm "$work/mnt/fill"
sync
verify
umount "$work/mnt"
mount "$first" "$work/mnt"
verify
btrfs scrub start -B "$work/mnt"
echo 'PASS: virtual Btrfs conversion, snapshot, missing disk, replacement, remount, ENOSPC and hashes'
