#!/bin/bash
set -e

OUTPUT="${1:-cloud-init.iso}"
CONFIG="${2:-cloud-init-config.yaml}"
HOSTNAME="${3:-}"
MOTD_FILE="${4:-}"  # optional: path to a file whose contents become /etc/motd

if [ ! -f "$CONFIG" ]; then
    echo "Error: cloud-init config file not found: $CONFIG" >&2
    exit 1
fi

# Create temporary directory structure
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

mkdir -p "$TMPDIR/nocloud"

# Generate instance-id (use hostname if provided)
if [ -n "$HOSTNAME" ]; then
    INSTANCE_ID="iid-$HOSTNAME"
else
    INSTANCE_ID="iid-firecracker-vm"
fi

cat > "$TMPDIR/nocloud/meta-data" << EOF
instance-id: $INSTANCE_ID
hostname: ${HOSTNAME:-firecracker-vm}
EOF

# If hostname is provided, inject it into the config
if [ -n "$HOSTNAME" ]; then
    USERDATA=$(mktemp)
    trap "rm -f $USERDATA" EXIT
    {
        cat "$CONFIG" | sed "s/CLOUDHOST/$HOSTNAME/g"
    } > "$USERDATA"
    cp "$USERDATA" "$TMPDIR/nocloud/user-data"
else
    # Replace CLOUDHOST with a default if no hostname provided
    sed "s/CLOUDHOST/firecracker-vm/g" "$CONFIG" > "$TMPDIR/nocloud/user-data"
fi

# If a MOTD file is provided, append extra runcmd steps that write the
# scenario brief to /etc/motd and install a profile.d hook so it prints
# on every interactive login.  We use runcmd (not write_files) because
# the Firecracker CI image may not have the write_files cloud-init module
# enabled; runcmd is always active.
if [ -n "$MOTD_FILE" ] && [ -f "$MOTD_FILE" ]; then
    MOTD_B64=$(base64 -w0 < "$MOTD_FILE")
    # runcmd is the last top-level key so appending list items extends it.
    printf "  - echo %s | base64 -d > /etc/motd\n"                               "$MOTD_B64" >> "$TMPDIR/nocloud/user-data"
    printf "  - chmod 0644 /etc/motd\n"                                                        >> "$TMPDIR/nocloud/user-data"
    printf "  - echo '#!/bin/sh' > /etc/profile.d/99-scenario.sh\n"                           >> "$TMPDIR/nocloud/user-data"
    printf "  - echo 'cat /etc/motd 2>/dev/null' >> /etc/profile.d/99-scenario.sh\n"          >> "$TMPDIR/nocloud/user-data"
    printf "  - chmod 0755 /etc/profile.d/99-scenario.sh\n"                                   >> "$TMPDIR/nocloud/user-data"
fi

# Create ISO using mkisofs
mkisofs -output "$OUTPUT" -V CIDATA -J -R "$TMPDIR" > /dev/null 2>&1

echo "Created cloud-init ISO: $OUTPUT"
