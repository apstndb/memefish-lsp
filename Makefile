build:
	go build ./
gen:
	(cd protocol ; ../../../gostaticanalysis/knife/hagane -template ../lspabst/gen.go.tmpl /dev/null > ../lspabst/gen.go)
