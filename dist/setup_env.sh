#!/bin/bash

GHREPO="https://github.com/${TRAVIS_REPO_SLUG}"
GHREPO_NAME=$(basename ${TRAVIS_REPO_SLUG})
TARGET_ARCH=x86_64-pc-linux-gnu
