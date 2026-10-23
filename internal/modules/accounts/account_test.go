package accounts

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMinorExact(t *testing.T) {
	for _, test := range []struct{
		raw string
		value int64
	}{
		{"0",0},{"0.00",0},{"0.1",10},{"19.99",1999},
		{"1234.56",123456},{"999999999999.99",MaxOpeningMinor},
	} {
		actual,err:=ParseMinor(test.raw)
		if err!=nil || actual!=test.value {t.Fatalf("%s: got %d, %v",test.raw,actual,err)}
	}
}
func TestParseMinorRejectsInvalid(t *testing.T) {
	for _,raw:=range []string{"","-1","1.001","0.000","1,000.00","1e5",
		"1000000000000.00","999999999999.999","+2","NaN","1.2.3","AED 2"} {
		if _,err:=ParseMinor(raw);!errors.Is(err,ErrInvalid){t.Fatalf("unexpected accepted %q",raw)}
	}
}
func TestValidateAccount(t *testing.T){
	k,err:=NewSetupKey()
	if err!=nil||!IsValidKey(k){t.Fatal("invalid key")}
	input:=CreateInput{Name:" UAE bank ",Kind:"BANK",Currency:"aed",Opening:"23.50",SetupKey:k}
	got,n,err:=Validate(input)
	if err!=nil||n!=2350||got.Name!="UAE bank"||got.Kind!="bank"||got.Currency!="AED" {t.Fatalf("%+v %d %v",got,n,err)}
	for _,bad:=range []CreateInput{
		{Name:"",Kind:"bank",Currency:"AED",Opening:"0",SetupKey:k},
		{Name:strings.Repeat("z",81),Kind:"bank",Currency:"AED",Opening:"0",SetupKey:k},
		{Name:"Test",Kind:"crypto",Currency:"AED",Opening:"0",SetupKey:k},
		{Name:"Test",Kind:"cash",Currency:"USD",Opening:"0",SetupKey:k},
		{Name:"Test",Kind:"credit",Currency:"AED",Opening:"-10",SetupKey:k},
		{Name:"Test",Kind:"bank",Currency:"AED",Opening:"0",SetupKey:"foo"},
	} {
		if _,_,err:=Validate(bad);!errors.Is(err,ErrInvalid){t.Fatalf("accepted %+v",bad)}
	}
}
func TestDisplayAndNature(t *testing.T){
	if FormatMinor(123456789)!="1,234,567.89" {t.Fatal("formatting")}
	if FormatMinor(0)!="0.00" {t.Fatal("zero")}
	for _,k:=range []string{"credit","loan"} {
		if Nature(k)!="liability" {t.Fatalf("%s should be liability",k)}
	}
	if Nature("bank")!="asset" || KindLabel("ewallet")!="E-wallet" {t.Fatal("asset mapping")}
}
