VERSION ?= 0.1.0
APP     := build/diskpy.app

.PHONY: build app run test clean

build:
	go build -o diskpy .

# app builds a double-clickable bundle, signed ad hoc (no Developer ID).
app:
	rm -rf $(APP) build/icon.iconset
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources build/icon.iconset
	go build -ldflags "-s -w" -o $(APP)/Contents/MacOS/diskpy .
	sed 's/@VERSION@/$(VERSION)/g' packaging/Info.plist > $(APP)/Contents/Info.plist
	for s in 16 32 128 256 512; do \
		sips -z $$s $$s packaging/Icon.png --out build/icon.iconset/icon_$${s}x$${s}.png >/dev/null; \
		sips -z $$((s*2)) $$((s*2)) packaging/Icon.png --out build/icon.iconset/icon_$${s}x$${s}@2x.png >/dev/null; \
	done
	iconutil -c icns build/icon.iconset -o $(APP)/Contents/Resources/icon.icns
	rm -rf build/icon.iconset
	codesign --force --sign - $(APP)
	@echo "Listo: $(APP)"

run: app
	open $(APP)

test:
	go test -race ./...

clean:
	rm -rf build diskpy
