package data_test

import (
	"fmt"

	"github.com/go-theft-craft/minecraft-protocol/data"
	_ "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
)

func Example_loadGeneratedJavaData() {
	set, err := data.Load("java/1.8.9")
	if err != nil {
		panic(err)
	}

	fmt.Println(set.Version().Protocol)
	// Output: 47
}
