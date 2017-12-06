package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"sync"

	apisupport "github.com/go-accounting/api-support"
	"github.com/go-accounting/fde"
	"github.com/julienschmidt/httprouter"
)

var api *apisupport.Api

type repository struct {
	*fde.TxsRepository
	user  string
	coaid string
}

var repositoryPool = sync.Pool{
	New: func() interface{} {
		r := &repository{}
		v, err := api.Config().Run("NewFdeStoreAndAccountsRepository", &r.user, &r.coaid)
		if err != nil {
			panic(err)
		}
		r.TxsRepository = fde.NewTxsRepository(
			v.([]interface{})[0].(fde.Store),
			v.([]interface{})[1].(fde.AccountsRepository),
		)
		return r
	},
}

func handler(
	f func(*repository, httprouter.Params, apisupport.Decoder) (interface{}, error),
) func(http.ResponseWriter, *http.Request, httprouter.Params) {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		user := api.UserFromRequest(w, r)
		if user == "" {
			return
		}
		tr := repositoryPool.Get().(*repository)
		tr.user = user
		tr.coaid = ps.ByName("coa")
		defer repositoryPool.Put(tr)
		api.Run(w, func() (interface{}, error) {
			return f(tr, ps, func(v interface{}) error {
				return api.Decode(r, v)
			})
		})
	}
}

func saveTransaction(r *repository, ps httprouter.Params, d apisupport.Decoder) (interface{}, error) {
	var txs []*fde.Transaction
	if err := d(&txs); err != nil {
		return nil, err
	}
	return r.Save(txs...)
}

func getTransaction(r *repository, ps httprouter.Params, _ apisupport.Decoder) (interface{}, error) {
	return r.Get(ps.ByName("txid"))
}

func updateTransaction(r *repository, ps httprouter.Params, d apisupport.Decoder) (interface{}, error) {
	var tx *fde.Transaction
	if err := d(&tx); err != nil {
		return nil, err
	}
	tx.Id = ps.ByName("txid")
	return r.Save(tx)
}

func deleteTransaction(r *repository, ps httprouter.Params, _ apisupport.Decoder) (interface{}, error) {
	return r.Delete(ps.ByName("txid"))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("usage: %v settings", path.Base(os.Args[0]))
		return
	}
	var err error
	api, err = apisupport.New(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	router := httprouter.New()
	router.POST("/api/fde/:coa/transactions", handler(saveTransaction))
	router.GET("/api/fde/:coa/transactions/:txid", handler(getTransaction))
	router.PUT("/api/fde/:coa/transactions/:txid", handler(updateTransaction))
	router.DELETE("/api/fde/:coa/transactions/:txid", handler(deleteTransaction))
	log.Fatal(http.ListenAndServe(":8080", router))
}
