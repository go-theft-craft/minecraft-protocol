package data_test

import (
	"fmt"

	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
)

func Example_directGeneratedJavaData() {
	set, err := v1_8.Data()
	if err != nil {
		panic(err)
	}

	fmt.Println(set.Version().MinecraftVersion)
	// Output: 1.8.8
}
