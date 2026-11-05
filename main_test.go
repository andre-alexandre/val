package val

import (
	"fmt"
	"testing"
)

var Val *Validator

type User struct {
	Age         uint8  `val:"self >= 18 && self <= 130" valerr:"The Users must be between 18 and 130 years old"`
	Username    string `val:"self.matches('^[a-zA-Z]+$')" valerr:"The Users' username must only contain normal letters"`
	Description string `val:"self != ''" valerr:"The Users' description cannot be empty"`
}

func BenchmarkValidate(b *testing.B) {
	var err error
	if Val, err = CreateValidator(User{}); err != nil {
		panic(fmt.Errorf("initialize validator: %w", err))
	}

	user := User{
		Age:         25,
		Username:    "justnormalletters",
		Description: "Is not empty!",
	}

	b.ReportAllocs()
	for b.Loop() {
		if err := Val.Validate(user); err != nil {
			b.Fatal(err)
		}
	}
}
