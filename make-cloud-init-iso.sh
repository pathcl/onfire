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

# If a MOTD file is provided, embed it into the ISO via cloud-init write_files.
# The content is base64-encoded to safely handle special characters.
# We also add /etc/profile.d/99-scenario.sh so the brief prints on every
# interactive login regardless of PAM motd configuration.
if [ -n "$MOTD_FILE" ] && [ -f "$MOTD_FILE" ]; then
    MOTD_B64=$(base64 -w0 < "$MOTD_FILE")
    cat >> "$TMPDIR/nocloud/user-data" << EOF

write_files:
  - path: /etc/motd
    encoding: b64
    content: $MOTD_B64
  - path: /etc/profile.d/99-scenario.sh
    content: |
      #!/bin/sh
      cat /etc/motd 2>/dev/null
    permissions: '0755'
EOF
fi

# Create ISO using mkisofs
mkisofs -output "$OUTPUT" -V CIDATA -J -R "$TMPDIR" > /dev/null 2>&1

echo "Created cloud-init ISO: $OUTPUT"
