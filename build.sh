#!/bin/sh
# Compila mterm come binario statico, senza dipendere da make.
# Uso: ./build.sh [install]
set -e
cd "$(dirname "$0")"

# Go: quello nel PATH, altrimenti l'installazione in home.
GO="$(command -v go || echo "$HOME/.local/go/bin/go")"
[ -x "$GO" ] || { echo "Go non trovato (né nel PATH né in ~/.local/go)"; exit 1; }

CGO_ENABLED=0 "$GO" build -ldflags="-s -w" -o mterm .
echo "fatto: $(pwd)/mterm ($(du -h mterm | cut -f1)), statico"

if [ "$1" = "install" ]; then
	BINDIR="${PREFIX:-$HOME/.local}/bin"
	mkdir -p "$BINDIR"
	cp mterm "$BINDIR/mterm"
	echo "installato in $BINDIR/mterm"
fi
