package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	fmt.Println("Mender mock")
	for i := 0; i < 3; i++ {
		fmt.Println(i)
		time.Sleep(1 * time.Second)
	}
	os.Exit(0)
}
