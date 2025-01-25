# Contributing

We welcome contributions from the community. Please read the following guidelines carefully to maximize the chances of your PR being merged.

# Local development of Envoy AI Gateway project

First of all, there are only two minimal prerequisites to contribute to the project:
* The latest Go toolchain.
* `make` commands.

which we assume you already have installed on your machine.

Assuming you have already cloned the repository on a local machine either MacOS or some Linux distribution,
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
All test targets are prefixed with `test-*` and can be run via `make test-<target>`.

Some test commands might require additional dependencies to be installed on your machine.
For example,

* The latest `kubectl` binary for running `make test-e2e`.
* The latest `envoy` binary for running `make test-extproc`.

Other than that, everything will be automatically managed and installed via `make` targets,
and you should not need to worry about the dependencies (tell us if you do).

Additionally, some of the test cases in `test-e2e` and `test-extproc` might require some credentials.
You will find which credentials are required in the output of the test command. All test cases requiring
credentials are skipped by default when the credentials are not provided. If you
want to run these tests locally, please prepare the necessary credentials by yourself.

## DCO

We require DCO signoff line in every commit to this repo.

The sign-off is a simple line at the end of the explanation for the
patch, which certifies that you wrote it or otherwise have the right to
pass it on as an open-source patch. The rules are pretty simple: if you
can certify the statement in [developercertificate.org](https://developercertificate.org/)
then you just add a line to every git commit message:

    Signed-off-by: Joe Smith <joe@gmail.com>

using your real name (sorry, no pseudonyms or anonymous contributions.)

You can add the sign off when creating the git commit via `git commit -s`.

## Code Reviews

* The pull request title should describe what the change does and not embed issue numbers.
The pull request should only be blank when the change is minor. Any feature should include
a description of the change and what motivated it. If the change or design changes through
review, please keep the title and description updated accordingly.
* **A single approval is sufficient to merge**. If a reviewer asks for
changes in a PR they should be addressed before the PR is merged,
even if another reviewer has already approved the PR.
* During the review, address the comments and commit the changes
**without squashing the commits**. This facilitates incremental reviews
since the reviewer does not go through all the code again to find out
what has changed since the last review. When a change goes out of sync with main,
please rebase and force push, keeping the original commits where practical.
* Commits are squashed prior to merging a pull request, using the title and PR description
as commit message by default. Maintainers may request contributors to
edit the pull request title and description to ensure that it remains descriptive as a
commit message. Alternatively, maintainers may change the commit message directly at the time of merge.
