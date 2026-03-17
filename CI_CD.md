# CI/CD

## What Was Added

- `.github/workflows/ci.yml`
  - runs `go test ./...`
  - builds `./cmd/tcpadapter`
  - builds `./cmd/controller-sim`
  - verifies Docker image build

- `.github/workflows/deploy.yml`
  - runs on push to `main`
  - builds and pushes adapter image to `ghcr.io`
  - uploads `docker-compose.yml` to the server
  - deploys the new image over SSH
  - verifies `http://127.0.0.1:18080/readyz`

## Required GitHub Secrets

Add these repository secrets in GitHub:

- `SSH_HOST`
- `SSH_USER`
- `SSH_PASSWORD`
- `GHCR_USERNAME`
- `GHCR_TOKEN`

Notes:

- `GHCR_TOKEN` should be a token that can pull packages from `ghcr.io` on the server.
- If you later switch to SSH keys, replace `SSH_PASSWORD` in the workflows with a private key secret.

## Deploy Flow

1. Push code to a branch or open a PR.
2. `CI` validates tests, binaries, and Docker build.
3. Merge to `main`.
4. `Deploy` builds image `ghcr.io/<owner>/tcpadapter:<commit_sha>`.
5. Workflow uploads `docker-compose.yml` to `/opt/tcpadapter`.
6. Workflow connects to the server and runs:
   - `docker login ghcr.io`
   - `docker compose pull adapter`
   - `docker compose up -d adapter`
7. Workflow checks `readyz`.

## Local Development

Local development still works with:

```bash
docker compose up -d --build
```

because `docker-compose.yml` keeps the local fallback:

```yaml
image: ${TCPADAPTER_IMAGE:-tcpadapter:local}
```

