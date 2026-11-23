# Container fixtures

Recorded structure, synthetic values. `podman-df.json` is `podman system df --format json` (Podman 5.8.2, sizes only);
`podman-ps-a.json` and `podman-machine-list.json` keep the real field names and types of `podman ps -a --format json` and
`podman machine list --format json`, with made-up names, ids and paths. The fixed "now" for the ps fixture is epoch
1760000000 (2025-10-09): `fixture-old-nomount` exited 45 days before (candidate), `fixture-recent` 2 days before (kept),
`fixture-old-with-mount` 60 days before with a mount (kept), `fixture-running` is running (kept). No Docker output was
recorded (Docker is not installed on the recording machine): Docker's `system df --format json` differs from Podman's, so
the Docker parser is tested only against the documented shape and must treat anything unexpected as "engine unavailable".
