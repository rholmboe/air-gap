package protocol

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

func CompressGzip(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write(payload)
	if err != nil {
		return nil, err
	}
	err = writer.Close()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecompressGzip(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return decompressed, nil
}
