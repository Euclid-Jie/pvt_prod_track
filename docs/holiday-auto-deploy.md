# Holiday file deployment to Zeus

`nav_interval_metric/Chinese_special_holiday.txt` is the source of truth. A push
to `master` that changes this file runs the GitHub Actions workflow in that
repository. The workflow validates the file, then connects to Zeus with a
dedicated SSH key and streams the file to the restricted update command.

The Zeus account is `pvt-deploy`. Its authorized key is forced to run
`sudo -n /usr/local/sbin/pvt-update-holiday` and cannot open a shell or forward
ports. The sudoers rule permits only that command with no arguments. The
updater validates every date, creates a timestamped backup, atomically installs
the new file as `pvt-prod-track:pvt-prod-track` with mode `0600`, and restarts
`pvt-prod-track.service` only when the content changed. If the restart fails,
it restores the previous file and attempts to restart the service again.

The updater source is [`deploy/pvt-update-holiday`](../deploy/pvt-update-holiday).
Install it on Zeus as root at `/usr/local/sbin/pvt-update-holiday`, owned by
root and mode `0755`. Install the dedicated account and sudoers restriction
with [`deploy/install-pvt-deploy-access.sh`](../deploy/install-pvt-deploy-access.sh),
passing the deployment public key. Keep the production file at
`/var/lib/pvt-prod-track/Chinese_special_holiday.txt`; it takes precedence over
the holiday file embedded in the binary.

The workflow uses the `production` GitHub Environment. Its `ZEUS_SSH_PRIVATE_KEY`
secret contains only the dedicated deployment key. `ZEUS_HOST`,
`ZEUS_DEPLOY_USER`, and `ZEUS_KNOWN_HOSTS` are environment variables; the host
key must be pinned from a trusted existing SSH `known_hosts` entry.

To disable automatic deployment, disable the workflow or remove the SSH key
from the `pvt-deploy` account. To roll back, restore a timestamped
`Chinese_special_holiday.txt.before-*` backup and restart
`pvt-prod-track.service`.
