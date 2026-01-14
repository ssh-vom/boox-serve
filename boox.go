package main

import (
	"fmt"
	"os"
)

func queryBoox() {
	boox_ip := os.Getenv("BOOX_TABLET_IP")
	fmt.Println(boox_ip)
}
