package main

import (
	"fmt"
	"github.com/ssdev-go/go-tagexpr/v2"
)

func main() {
	type T struct {
		A  int
		B  string
		C  bool
		d  []string
		e  map[string]int
		e2 map[string]*int
		f  struct {
			g int `tagexpr:"$"`
		}
	}
	vm := tagexpr.New("tagexpr")
	t := &T{
		A:  50,
		B:  "abc",
		C:  true,
		d:  []string{"x", "y"},
		e:  map[string]int{"len": 1},
		e2: map[string]*int{"len": new(int)},
		f: struct {
			g int `tagexpr:"$"`
		}{1},
	}
	var expr = "$<0||$>=100"
	exprs := make(map[string]string)
	exprs["A"] = expr
	exprs["f.g"] = "$"
	exprs["C"] = "expr1:(f.g)$>0 && $; expr2:'C must be true when T.f.g>0'\""
	tagExpr, err := vm.RunByExpr(t, exprs)
	if err != nil {
		panic(err)
	}

	fmt.Println(tagExpr.Eval("A"))
	fmt.Println(tagExpr.Eval("C@expr1"))
	fmt.Println(tagExpr.Eval("C@expr2"))
	fmt.Println(tagExpr.Eval("f.g"))
}
