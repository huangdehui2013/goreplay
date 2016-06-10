package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"strconv"
	"time"
)

// FileInput can read requests generated by FileOutput
type FileInput struct {
	data             chan []byte
	exit             chan bool
	path             string
	fileInputReaders []*fileInputReader
	speedFactor      float64
	loop             bool
}


type fileInputReader struct {
	reader    *bufio.Reader
	meta      [][]byte
	data      []byte
	file      *os.File
	timestamp int64
}

// NewFileInput constructor for FileInput. Accepts file path as argument.
func NewFileInput(path string, loop bool) (i *FileInput) {
	i = new(FileInput)
	i.data = make(chan []byte)
	i.exit = make(chan bool)
	i.path = path
	i.speedFactor = 1
	i.loop = loop

	if err := i.init(); err != nil {
		return
	}

	go i.emit()

	return
}

type NextFileNotFound struct{}

func (_ *NextFileNotFound) Error() string {
	return "There is no new files"
}

func (i *FileInput) init() (err error) {
	var matches []string

	if matches, err = filepath.Glob(i.path); err != nil {
		log.Println("Wrong file pattern", i.path, err)
		return
	}

	if len(matches) == 0 {
		log.Println("No files match pattern: ", i.path)
		return errors.New("No matching files")
	}

	i.fileInputReaders = make([]*fileInputReader, len(matches))

	for idx, p := range matches {
		file, _ := os.Open(p)
		fileInputReader := &fileInputReader{}
		fileInputReader.file = file
		if strings.HasSuffix(p, ".gz") {
			gzReader, err := gzip.NewReader(file)
			if err != nil {
				log.Fatal(err)
			}
			fileInputReader.reader = bufio.NewReader(gzReader)
		} else {
			fileInputReader.reader = bufio.NewReader(file)
		}

		fileInputReader.readNextInput()
		i.fileInputReaders[idx] = fileInputReader
	}

	return nil
}

func (i *FileInput) Read(data []byte) (int, error) {
	buf := <-i.data
	copy(data, buf)

	return len(buf), nil
}

func (i *FileInput) String() string {
	return "File input: " + i.path
}

func (f *fileInputReader) readNextInput() {
	nextInput := f.nextInput()
	f.parseNextInput(nextInput)
}

func (f *fileInputReader) parseNextInput(input []byte) {
	if (input != nil) {
		f.meta = payloadMeta(input)
		f.timestamp, _ = strconv.ParseInt(string(f.meta[2]), 10, 64)
		f.data = input
	}
}

func (f *fileInputReader) nextInput() []byte {
	payloadSeparatorAsBytes := []byte(payloadSeparator)
	var buffer bytes.Buffer

	for {
		line, err := f.reader.ReadBytes('\n')

		if err != nil {
			if err != io.EOF {
				log.Fatal(err)
			}

			if err == io.EOF {
				f.file.Close()
				f.file = nil
				return nil
			}
		}

		if bytes.Equal(payloadSeparatorAsBytes[1:], line) {
			asBytes := buffer.Bytes()

			// Bytes() returns only pointer, so to remove data-race copy the data to an array
			newBuf := make([]byte, len(asBytes) - 1)
			copy(newBuf, asBytes)
			return newBuf
		}

		buffer.Write(line)
	}
}

func (i *FileInput) nextInputReader() *fileInputReader {
	var nextFileInputReader *fileInputReader
	for _, fileInputReader := range i.fileInputReaders {
		if fileInputReader.file == nil {
			continue
		}

		if fileInputReader.meta[0][0] == ResponsePayload {
			return fileInputReader
		}

		if nextFileInputReader == nil || nextFileInputReader.timestamp > fileInputReader.timestamp {
			nextFileInputReader = fileInputReader
			continue
		}
	}

	return nextFileInputReader;
}

func (i *FileInput) emit() {
	var lastTime int64 = -1

	for {
		fileInputReader := i.nextInputReader()

		if fileInputReader == nil {
			if i.loop {
				i.init()
				lastTime = -1
				continue
			} else {
				break;
			}
		}

		if fileInputReader.meta[0][0] == RequestPayload {
			lastTime = i.simulateRequestDelay(fileInputReader, lastTime)
		}

		select {
		case <-i.exit:
			for _, fileInputReader := range i.fileInputReaders {
				if fileInputReader.file != nil {
					fileInputReader.file.Close()
				}
			}
			break
		case i.data <- fileInputReader.data:
			fileInputReader.readNextInput()
		}
	}

	log.Printf("FileInput: end of file '%s'\n", i.path)
}

func (i*FileInput) simulateRequestDelay(fileInputReader *fileInputReader, lastTime int64) int64 {
	if lastTime != -1 {
		timeDiff := fileInputReader.timestamp - lastTime

		if i.speedFactor != 1 {
			timeDiff = int64(float64(timeDiff) / i.speedFactor)
		}

		time.Sleep(time.Duration(timeDiff))
	}

	return fileInputReader.timestamp
}

func (i *FileInput) Close() error {
	i.exit <- true
	return nil
}

