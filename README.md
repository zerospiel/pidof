# pidof

[![Codecov](https://codecov.io/gh/zerospiel/pidof/graph/badge.svg)](https://codecov.io/gh/zerospiel/pidof)
[![CI](https://github.com/zerospiel/pidof/actions/workflows/ci.yml/badge.svg)](https://github.com/zerospiel/pidof/actions/workflows/ci.yml)

Display the PID number for a given process name(s).

Drop-in replacement for the deprecated and removed [NightProductions CLI](https://web.archive.org/web/20240808152721/http://www.nightproductions.net/cli.htm).

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
