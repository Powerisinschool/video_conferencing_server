all: build

build:
	go build -o video_conferencing_server main.go

run: build
	./video_conferencing_server

clean:
	rm -f video_conferencing_server
