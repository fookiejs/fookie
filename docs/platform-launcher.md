# Fookie Platform Launcher

Run the full platform as a single `docker run` entrypoint while keeping services split internally.

## Build image

```bash
docker build -f docker/Dockerfile.platform -t fookie/platform .
```

## Run commands

```bash
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock fookie/platform start
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock fookie/platform status
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock fookie/platform logs
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock fookie/platform stop
```

## Profiles

- `PROFILE=full` (default): core + observability
- `PROFILE=minimal`: core only

```bash
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock -e PROFILE=minimal fookie/platform start
```

## Scale mode

```bash
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock -e SERVERS=5 -e WORKERS=20 fookie/platform scale
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock fookie/platform down-scale
```

## Notes

- Launcher uses host Docker through socket mount.
- This is intended for local/dev usage.
- Production rollout should be handled through Rancher + Fleet + Helm.
