VERSION   ?= 0.1.0
APP       := build/diskpy.app
MIN_MACOS := 13.0

CGO_CFLAGS   ?= -O2 -g
CGO_CXXFLAGS ?= -O2 -g
CGO_LDFLAGS  ?= -O2 -g
TARGET_FLAG  := -mmacosx-version-min=$(MIN_MACOS)

.PHONY: build app run test test-integration clean

build:
	go build -o diskpy .

# app builds a double-clickable bundle, signed ad hoc (no Developer ID).
app:
	rm -rf $(APP) build/icon.iconset
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources build/icon.iconset
	CGO_CFLAGS="$(CGO_CFLAGS) $(TARGET_FLAG)" \
	CGO_CXXFLAGS="$(CGO_CXXFLAGS) $(TARGET_FLAG)" \
	CGO_LDFLAGS="$(CGO_LDFLAGS) $(TARGET_FLAG)" \
	go build -ldflags "-s -w" -o $(APP)/Contents/MacOS/diskpy .
	sed -e 's/@VERSION@/$(VERSION)/g' -e 's/@MIN_MACOS@/$(MIN_MACOS)/g' packaging/Info.plist > $(APP)/Contents/Info.plist
	for s in 16 32 128 256 512; do \
		sips -z $$s $$s packaging/Icon.png --out build/icon.iconset/icon_$${s}x$${s}.png >/dev/null; \
		sips -z $$((s*2)) $$((s*2)) packaging/Icon.png --out build/icon.iconset/icon_$${s}x$${s}@2x.png >/dev/null; \
	done
	iconutil -c icns build/icon.iconset -o $(APP)/Contents/Resources/icon.icns
	rm -rf build/icon.iconset
	codesign --force --sign - $(APP)
	@echo "Built $(APP)"

run: app
	open $(APP)

test:
	go test -race -short ./...

test-integration:
	go test -race ./...

clean:
	rm -rf build diskpy
