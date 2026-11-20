# go-tiled

[![PkgGoDev](https://pkg.go.dev/badge/github.com/lafriks/go-tiled)](https://pkg.go.dev/github.com/lafriks/go-tiled)
[![Build Status](https://cloud.drone.io/api/badges/lafriks/go-tiled/status.svg?ref=refs/heads/master)](https://cloud.drone.io/lafriks/go-tiled)

Go library to parse Tiled map editor file format (TMX) and render map to image. Currently supports rendering of orthogonal finite maps out-of-the-box; infinite maps are parsed but cannot be rendered as a single image.

## Infinite Maps

Infinite maps store their tile layers as rectangular `<chunk>`s at arbitrary (possibly negative) global grid coordinates. Layers of such maps have an empty `Tiles` slice; their data is exposed as `Layer.Chunks`, and tiles are queried by global coordinate:

```go
tile := layer.TileAt(-3, 12)              // NilLayerTile outside all chunks
tiles := layer.TileRegion(-16, -16, 32, 32) // row-major, 32 entries per row
```

`TileAt` and `TileRegion` also work on finite maps (where they index the same data as `Tiles`), so callers can use them uniformly. Chunks must not partially overlap; identical duplicate chunks are accepted with the last one winning.

## Installing

```sh
go get github.com/lafriks/go-tiled
```

You can use `go get -u` to update the package. You can also just import and start using the package directly if you're using Go modules, and Go will then download the package on first compilation.

## Basic Usage

```go
package main

import (
    "fmt"
    "os"

    "github.com/lafriks/go-tiled"
    "github.com/lafriks/go-tiled/render"
)

const mapPath = "maps/map.tmx" // Path to your Tiled Map.

func main() {
    // Parse .tmx file.
    gameMap, err := tiled.LoadFile(mapPath)
    if err != nil {
        fmt.Printf("error parsing map: %s", err.Error())
        os.Exit(2)
    }

    fmt.Println(gameMap)

    // You can also render the map to an in-memory image for direct
    // use with the default Renderer, or by making your own.
    renderer, err := render.NewRenderer(gameMap)
    if err != nil {
        fmt.Printf("map unsupported for rendering: %s", err.Error())
        os.Exit(2)
    }

    // Render just layer 0 to the Renderer.
    err = renderer.RenderLayer(0)
    if err != nil {
        fmt.Printf("layer unsupported for rendering: %s", err.Error())
        os.Exit(2)
    }

    // Get a reference to the Renderer's output, an image.NRGBA struct.
    img := renderer.Result

    // Clear the render result after copying the output if separation of
    // layers is desired.
    renderer.Clear()

    // And so on. You can also export the image to a file by using the
    // Renderer's Save functions.
}
```
### Embedded Filesystems
To work with embedded file systems via the [embed package](https://pkg.go.dev/embed#FS), both the tilemap and renderer need to be initialized WithFileSystem.
```go
tm, err := tiled.LoadFile(mapPath, tiled.WithFileSystem(assets.Files))
...
r, err := render.NewRendererWithFileSystem(tm, assets.Files)

```

## Documentation

For further documentation, see <https://pkg.go.dev/github.com/lafriks/go-tiled> or run:

```sh
godoc github.com/lafriks/go-tiled
```
