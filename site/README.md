# Website

This website is built using [Docusaurus](https://docusaurus.io/), a modern static website generator.

### Local Development

### Requirements {#requirements}

- [Node.js](https://nodejs.org/en/download/) version 18.0 or above (which can be checked by running `node -v`). You can use [nvm](https://github.com/nvm-sh/nvm) for managing multiple Node versions on a single machine installed.
  - When installing Node.js, you are recommended to check all checkboxes related to dependencies.

### Install

```
npm install
```

### Run locally

#### NPX

```
npx docusaurus start
```

#### NPM

```
npm run start
```

#### **When to Use Which?**

- Use npx docusaurus start:
  - For quick tests or temporary runs without installing the Docusaurus CLI.
  - If you want to use the latest version of Docusaurus globally.
- Use npm run start:
  - For consistent and reproducible builds, ensuring you use the local version of Docusaurus.
  - In your development workflow, where the start script is part of your project setup.

### Updating versions

When generating a new version, edit the `versioned_docs/version-{version}/_vars.json` and set the right version values.

### AI Coding Assistants

This directory includes an `AGENTS.md` file with guidelines for AI coding assistants.
To enable it for your preferred tool, create a symlink:

**Cursor:**

```
ln -s AGENTS.md .cursorrules
```

**Claude Code:**

```
ln -s AGENTS.md CLAUDE.md
```
