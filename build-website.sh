#!/usr/bin/bash -e

build_site () {
    mkdir _site
    echo "HELO, WORLD" > _site/index.html
}

build_site
