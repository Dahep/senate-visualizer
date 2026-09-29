package main

import (
	"fmt"

	"congress-visualizer/internal/config"
	"congress-visualizer/internal/database"
)

func main() {
	cfg := config.Load()
	db, _ := database.Open(cfg.DBPath)
	defer db.Close()
	s := db.Raw()
	var n int64
	err := s.QueryRow("select count(*) from votations").Scan(&n)
	fmt.Println("plain:", n, err)
	err = s.QueryRow("select count(*) from votations v left join bills b on b.id=v.bill_id where ('' = '' or v.chamber_id=?)", "").Scan(&n)
	fmt.Println("join+?:", n, err)
	err = s.QueryRow("select count(*) from votations where (? = '' or chamber_id=?)", "", "camara").Scan(&n)
	fmt.Println("pair+?:", n, err)
}
