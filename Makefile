build:
	go build ./
gen:
	(cd protocol ; ../../../gostaticanalysis/knife/hagane -template ../gen.go.tmpl /dev/null > ../lspabst/gen.go)
