// Command mygo-emby is a desktop client for Emby and Jellyfin servers.
package main

import (
	"log"

	"mygo-emby/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
