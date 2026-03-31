# pidof

A small cross-UNIX `pidof`-like CLI for UNIX (mostly macOS).

It prints matching process IDs separated by spaces and exits with code `1` when no process matches.

## Usage

```bash
pidof <name> [name ...]
pidof --help
```

Examples:

```bash
pidof sshd
pidof bash zsh
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

Validate release config locally:

```bash
make validate
```
