# pidof

[![Codecov](https://codecov.io/gh/zerospiel/pidof/graph/badge.svg)](https://codecov.io/gh/zerospiel/pidof)
[![CI](https://github.com/zerospiel/pidof/actions/workflows/ci.yml/badge.svg)](https://github.com/zerospiel/pidof/actions/workflows/ci.yml)

Display the PID number for a given process name(s).

Drop-in replacement for the deprecated and removed [NightProductions CLI](https://web.archive.org/web/20240808152721/http://www.nightproductions.net/cli.htm).

## Installation

Homebrew:

```bash
brew tap zerospiel/tools
brew install --cask zerospiel/tools/pidof
```

Release installer:

```bash
curl -fsSL https://raw.githubusercontent.com/zerospiel/pidof/master/install.sh | bash
```

The installer supports `Darwin` and `Linux` on `amd64` and `arm64`. You can override the target directory or version:

```bash
curl -fsSL https://raw.githubusercontent.com/zerospiel/pidof/master/install.sh | INSTALL_DIR="$HOME/.local/bin" bash
curl -fsSL https://raw.githubusercontent.com/zerospiel/pidof/master/install.sh | VERSION=v0.1.2 bash
```

Build from source with Go:

```bash
go install github.com/zerospiel/pidof/cmd@latest
```

## Usage

```text
pidof <process-name>
```

Help:

```bash
pidof -?
```

Examples:

```bash
pidof sshd
pidof bash zsh
pidof -s bash
pidof -x my-script.sh
```

## Local development

```bash
make help
```

Build locally:

```bash
make build
```

Test:

```bash
make test
```

Validate release locally:

```bash
make validate
```
