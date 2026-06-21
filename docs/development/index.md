---
title: Development
sidebar_label: Overview
---

# Development

This section is for people hacking on Mekami itself: the maintainer, contributors adding a new language core, and future you when you've forgotten how the workspace is wired up.

The user-facing manual is the [user guide](../user-guide/index.md). This section is the "how do I work on this thing" manual.

import CardGrid, { Card } from '@site/src/components/CardGrid';

<CardGrid>
  <Card
    icon="🛠️"
    title="Setup"
    description="Prerequisites, repo layout, basic commands."
    to="/development/setup"
  />
  <Card
    icon="🧪"
    title="Testing"
    description="Two-tier test architecture, build tags, conventions, helpers."
    to="/development/testing"
  />
  <Card
    icon="📥"
    title="Contributing"
    description="Code style, PR process, the `all_gen` regeneration workflow."
    to="/development/contributing"
  />
  <Card
    icon="🏷️"
    title="Releasing"
    description="Lockstep tagging, version bumping, the AUR bump procedure."
    to="/development/releasing"
  />
</CardGrid>
