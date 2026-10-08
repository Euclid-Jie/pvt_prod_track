#!/usr/bin/env bash
set -Eeuo pipefail

pubkey_file=${1:?usage: install-pvt-deploy-access.sh PATH_TO_PUBLIC_KEY}
account=pvt-deploy

if [[ ! -f "$pubkey_file" ]]; then
  echo "public key file not found" >&2
  exit 1
fi

read -r key_type key_data key_comment < "$pubkey_file"
if [[ "$key_type" != ssh-ed25519 || -z "$key_data" ]]; then
  echo "expected one Ed25519 public key" >&2
  exit 1
fi

if ! id "$account" >/dev/null 2>&1; then
  useradd --create-home --home-dir "/home/$account" --shell /bin/sh --user-group "$account"
fi

install -d -o "$account" -g "$account" -m 0700 "/home/$account/.ssh"
printf 'restrict,command="/usr/bin/sudo -n /usr/local/sbin/pvt-update-holiday" %s %s %s\n' \
  "$key_type" "$key_data" "${key_comment:-pvt-prod-track-holiday-deploy}" \
  > "/home/$account/.ssh/authorized_keys"
chown "$account:$account" "/home/$account/.ssh/authorized_keys"
chmod 0600 "/home/$account/.ssh/authorized_keys"

printf '%s ALL=(root) NOPASSWD: /usr/local/sbin/pvt-update-holiday ""\n' "$account" \
  > /etc/sudoers.d/pvt-deploy-holiday
chmod 0440 /etc/sudoers.d/pvt-deploy-holiday
visudo -cf /etc/sudoers.d/pvt-deploy-holiday
