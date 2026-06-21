---
title: Build & install
sidebar_label: Overview
---

# Build & install

This section is for users who want to build Mekami from source, package it, or troubleshoot the build pipeline.

import CardGrid, { Card } from '@site/src/components/CardGrid';

<CardGrid>
  <Card
    icon="📦"
    title="From source"
    description="`./build.sh` mechanics, the `all_gen` cycle, Go toolchain requirements."
    to="/build/from-source"
  />
  <Card
    icon="🏛️"
    title="AUR packaging"
    description="The two AUR packages, the bump procedure, local sanity checks."
    to="/build/aur"
  />
</CardGrid>
