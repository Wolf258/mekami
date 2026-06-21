---
title: User guide
sidebar_label: Overview
---

# User guide

This section covers the user-facing surface of Mekami: the CLI, the MCP tools, configuration, the watch daemon, and the indexing pipeline.

import CardGrid, { Card } from '@site/src/components/CardGrid';

<CardGrid>
  <Card
    icon="🖥️"
    title="CLI reference"
    description="Every command Mekami exposes."
    to="/user-guide/cli"
  />
  <Card
    icon="🗄️"
    title="MCP tools"
    description="The 17 tools exposed over stdio."
    to="/user-guide/mcp-tools"
  />
  <Card
    icon="⚙️"
    title="Configuration"
    description="The `.mekami/config.json` schema."
    to="/user-guide/configuration"
  />
  <Card
    icon="👁️"
    title="Watch mode"
    description="Supervisor, watchdog, orphan adoption, inotify budget."
    to="/user-guide/watch-mode"
  />
  <Card
    icon="🕸️"
    title="How indexing works"
    description="From source files to the SQLite graph."
    to="/user-guide/how-it-works"
  />
</CardGrid>
