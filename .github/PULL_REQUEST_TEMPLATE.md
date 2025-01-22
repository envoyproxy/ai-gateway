<!--
⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️
Please make sure that at least `make precommit test` passes before submitting the PR as ready for review.
If there's anything you're unsure about, please ensure the PR is marked as a draft. For example,
draft PRs would be ideal for discussing the approach to a problem or the design of a feature.

Also, please make sure that you can check off all the items on the checklist below.

Otherwise, you might unnecessarily consume the time of the maintainers unless there's
a specific reason for not doing so.
⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️⚠️
-->

**Description / What this commit does / Why we need it**:

<!--
Please provide a brief description of the changes in this PR.
Example:

This allows configuring extproc to listen on an Unix domain socket. Note that
this commit just enables it in the extproc filter. Follow-up PRs will be
needed to accommodate that in the controller as well, but that would
require further changes to how the Gateway itself (not only the filter)
is deployed, to mount a common volume for the UDS.

Fixes #12345
-->

**Checklist**:
<!-- Remove items that do not apply. For completed items, change [ ] to [x]. -->

- [ ] The PR title follows the same format as the merged PRs.
- [ ] I have read the [CONTRIBUTING.md](../CONTRIBUTING.md) (for first-time contributors).
- [ ] I have made sure that `make precommit test` passes locally.
- [ ] I have written tests for any new line of code unless there's existing tests that cover the changes.
- [ ] I have updated the documentation accordingly.
- [ ] I am sure the coding style is consistent with the rest of the codebase.
