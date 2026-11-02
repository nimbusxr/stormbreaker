# stormbreaker

**stormbreaker** is an open-source framework for operational acceptance testing: smoke and load
tests in shared environments, where every team is answerable for the data its systems create.
It is built on [axx](https://github.com/nimbusxr/axx).

**Break it on purpose. Leave nothing behind.**

**Using stormbreaker?** Everything is at **[stormbreaker.nimbusxr.us](https://stormbreaker.nimbusxr.us)**:
installing it, the guides, and every step.

## Work on stormbreaker

You need [mise](https://mise.jdx.dev), and Docker for the integration tests.

```sh
git clone https://github.com/nimbusxr/stormbreaker && cd stormbreaker
mise install                                      # Go and the tools, at the versions the repository pins
go build -o bin/stormbreaker ./cmd/stormbreaker   # stormbreaker
mise run test                                     # go test -race ./...
mise run lint
```

- [AGENTS.md](AGENTS.md): where things are, and the rules every change follows.
- [CONTRIBUTING.md](CONTRIBUTING.md): sign off each commit (`git commit -s`), and title pull requests
  as conventional commits.
- Security issues: [SECURITY.md](SECURITY.md).

Apache-2.0, © NimbusXR.
