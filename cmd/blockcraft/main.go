// Command blockcraft launches the Blockcraft voxel game.
package main

import (
	"fmt"
	"os"

	"blockcraft-go/internal/app"
)

func main() {
	fmt.Fprintln(os.Stderr, "[blockcraft] starting")
	if err := app.Run(app.DefaultConfig()); err != nil {
		fmt.Fprintln(os.Stderr, "blockcraft:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "[blockcraft] exited cleanly")
}
