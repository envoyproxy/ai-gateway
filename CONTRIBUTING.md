# Contributing

We welcome contributions from the community. Please read the following guidelines carefully to maximize the chances of your PR being merged.

## Local development of Envoy AI Gateway project

First of all, there are only three minimal prerequisites to contribute to the project:

- The latest Go toolchain
- `make`
- `docker`

which we assume you already have installed on your machine. Also, on macOS, you would need to install
`brew install ca-certificates` to run some integration tests.

Assuming you have already cloned the repository on a local machine either macOS or some Linux distribution,
the only make targets you would need to run are listed via

```
make help
```

and everything will be done via `make` targets. You don't need to run anything else manually.
Anything necessary should go through `make` targets.

Please check out the output of the above command to see
the list of commands that you can run to build, test, and run the project.

For example, `make precommit test` will run the precommit checks and the unit tests.
These are the must-run commands before you submit or pushing commits to a PR.
If anything goes wrong, please try to run `make clean` and then run the command again.

You can make `make precommit` a git pre-commit hook by moving this file.

```shell
cp pre-commit.sh .git/hooks/pre-commit
```

All test targets are prefixed with `test-*` and can be run via `make test-<target>`.

Some test commands might require additional dependencies to be installed on your machine.
For example,

- The latest `kubectl` binary for running `make test-e2e`.
  - See: https://kubernetes.io/docs/tasks/tools/

Other than that, everything will be automatically managed and installed via `make` targets,
and you should not need to worry about the dependencies (tell us if you do).

Additionally, some of the test cases in `test-e2e` and `test-extproc` might require some credentials.
You will find which credentials are required in the output of the test command. All test cases requiring
credentials are skipped by default when the credentials are not provided. If you
want to run these tests locally, please prepare the necessary credentials by yourself.

## Opening a Pull Request

We employ almost the same guidelines as the [envoyproxy/envoy](https://github.com/envoyproxy/envoy/blob/main/CONTRIBUTING.md#submitting-a-pr).

### Ensure `make precommit` passes

As described above, please run `make precommit` locally before opening a PR and ensure that it passes.
If anything goes wrong, please do not open a PR or at least mark it as a draft PR. This is helpful to save time for the reviewers.

### Write Tests

We require tests for all new main code paths including any bug fixes.

### DCO

We require DCO signoff line in every commit to this repo.

The sign-off is a simple line at the end of the explanation for the
patch, which certifies that you wrote it or otherwise have the right to
pass it on as an open-source patch. The rules are pretty simple: if you
can certify the statement in [developercertificate.org](https://developercertificate.org/)
then you just add a line to every git commit message:

    Signed-off-by: Joe Smith <joe@gmail.com>

using your real name (sorry, no pseudonyms or anonymous contributions.)

You can add the signoff when creating the git commit via `git commit -s`.

### Title and Description

The title and description of the pull request is really important as they become a part of the commit message.
There's an automated GitHub action that checks for the various aspects of the PR title and description. Please
make sure that these automated checks pass after you open a PR by following the error messages.

### Code Reviews

- During the review, address the comments and commit the changes
  **without squashing the commits, force pushing, or rebasing the branch**.
  This facilitates incremental reviews since the reviewer does not go through all the code again to find out
  what has changed since the last review. When a change goes out of sych with master, please use `git merge`
  to avoid force pushing.
  - The only exception to this rule is when you mistakenly pushed a non-signoff commit.
    In this case, you can amend the commit with the signoff line and force push.
- Commits are squashed prior to merging a pull request, using the title and PR description
  as commit message by default. Maintainers may request contributors to
  edit the pull request title and description to ensure that it remains descriptive as a
  commit message. Alternatively, maintainers may change the commit message directly at the time of merge.
- If you have any questions during the review, please feel free to ask the maintainers listed in [MAINTAINERS.md](./MAINTAINERS.md).
