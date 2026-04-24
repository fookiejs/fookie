# FQL Language Extension

Syntax highlighting and language configuration for `.fql` files.

## Features

- Registers `.fql` as `FQL`
- TextMate grammar for core FQL syntax
- Bracket and auto-closing configuration

## Local packaging

Run in this folder:

```bash
npx @vscode/vsce package
```

This creates a `.vsix` file.

## Publish to Marketplace

Prerequisites:

- A VS Code Marketplace publisher (same as `publisher` in `package.json`)
- A Personal Access Token (PAT) for that publisher

Commands:

```bash
npx @vscode/vsce login fookie
npx @vscode/vsce publish
```

For version bumps:

```bash
npx @vscode/vsce publish patch
```
