package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

type Resource struct {
	Name string
}

func main() {
	m := make(map[string]Resource, 0)
	result := slices.SortedFunc(maps.Values(m), func(a, b Resource) int {
		return cmp.Compare(a.Name, b.Name)
	})
	fmt.Printf("result == nil: %v, len: %d\n", result == nil, len(result))

	type Result struct {
		Resources []Resource `json:"resources"`
	}

	r := Result{Resources: result}
	data, _ := json.Marshal(r)
	fmt.Printf("JSON with SortedFunc result: %s\n", data)

	r2 := Result{Resources: nil}
	data2, _ := json.Marshal(r2)
	fmt.Printf("JSON with nil:              %s\n", data2)

	r3 := Result{Resources: []Resource{}}
	data3, _ := json.Marshal(r3)
	fmt.Printf("JSON with empty slice:      %s\n", data3)
}
