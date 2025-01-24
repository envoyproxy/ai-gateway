#!/usr/bin/bash -e

build_site () {
    mkdir build
    echo "HELO, WORLD" > build/index.html
}

build_site
