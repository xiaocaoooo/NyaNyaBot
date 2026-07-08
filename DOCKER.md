# Docker Deployment Guide for NyaNyaBot

This guide explains how to deploy NyaNyaBot using Docker and Docker Compose.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/install/)

## Quick Start

1. **Clone the repository** (or navigate to the project root):
   ```bash
   git clone https://github.com/xiaocaoooo/NyaNyaBot.git
   cd NyaNyaBot
   ```

2. **Prepare data directory**:
   Create a `data` directory in the project root to store the configuration.
   ```bash
   mkdir data
   ```

3. **Run with Docker Compose**:
   ```bash
   docker compose up -d
   ```

The bot will be available at:
- **WebUI**: `http://localhost:3000`
- **OneBot Reverse WS**: `ws://localhost:3001`

## Configuration

NyaNyaBot uses a `config.json` file located in the `data/` directory.

- On the first run, the bot will automatically generate a random password for the WebUI and save it to `data/config.json`.
- You can check the logs to find the initial login URL or manually inspect `data/config.json`.
- To use a custom configuration, copy `config.example.json` to `data/config.json` and edit it before starting the container.

## Managing Plugins

Plugins are stored in the `plugins/` directory.

1. **Adding Plugins**:
   Place your compiled plugin binaries (prefixed with `nyanyabot-plugin-`) into the `./plugins` folder on your host machine.
   
2. **Reloading**:
   Restart the container to load new plugins:
   ```bash
   docker compose restart nyanyabot
   ```

## Advanced Usage

### Building the Image Manually

If you want to build the image without Docker Compose:

```bash
docker build -t nyanyabot .
docker run -d \
  -p 3000:3000 -p 3001:3001 \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/plugins:/app/plugins \
  --name nyanyabot \
  nyanyabot
```

### Updating NyaNyaBot

To update to the latest version:

```bash
docker compose pull # if using a remote image
git pull
docker compose up -d --build
```
