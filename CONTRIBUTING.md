# Contributing to Skael

Thanks for your interest in contributing to Skael! This guide will help you get started.

## Development Setup

Requires: Go 1.25+, Docker, [just](https://github.com/casey/just)

```bash
git clone https://github.com/skael-dev/skael.git
cd skael
cp .env.example .env         # configure local env vars
just db                      # start Postgres
just dev                     # run the server
just test                    # run all tests
```

Run `just` to see all available commands.

## Making Changes

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Write tests for your changes
4. Make your changes
5. Run `just check` to verify everything passes
6. Commit with conventional commit messages (`feat:`, `fix:`, `docs:`, etc.)
7. Open a pull request

## Code Style

- Follow existing patterns in the codebase
- Run `go vet ./...` and `gofmt` before committing
- Write table-driven tests where appropriate
- Keep packages focused and small

## Commit Messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — new feature
- `fix:` — bug fix
- `docs:` — documentation only
- `refactor:` — code change that neither fixes a bug nor adds a feature
- `test:` — adding or updating tests
- `chore:` — maintenance tasks

## Reporting Issues

Use GitHub Issues. Please include:

- Steps to reproduce
- Expected vs actual behavior
- Go version, OS, and Skael version

## License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License.
