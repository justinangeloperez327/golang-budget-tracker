package views

import (
 "net/url"
 "strconv"
 "github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)

func archivedSuffix(archived bool)string {
 if archived{return " (archived)"}
 return ""
}
func transactionsPageURL(filter transactions.Filter,page int)string {
 values:=url.Values{}
 if filter.Query!=""{values.Set("q",filter.Query)}
 if filter.Kind!=""{values.Set("kind",filter.Kind)}
 if filter.AccountID!=""{values.Set("account",filter.AccountID)}
 if filter.From!=""{values.Set("from",filter.From)}
 if filter.To!=""{values.Set("to",filter.To)}
 values.Set("page",strconv.Itoa(page))
 return "/transactions?"+values.Encode()
}
