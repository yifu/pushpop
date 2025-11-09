package transfer

import (
    "io"
    "log"
    "net"
    "os"
    "github.com/gosuri/uiprogress"
)

// Accept listens for connections and serves the given file.
func Accept(ln net.Listener, fn string) {
    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Fatal(err)
        }
        go ProcessConn(conn, fn)
    }
}

// ProcessConn sends the file over the connection, using a progress bar when possible.
func ProcessConn(conn net.Conn, fn string) {
    defer conn.Close()

    f, err := os.Open(fn)
    if err != nil {
        log.Println("Unable to open file:", err)
        return
    }
    defer f.Close()

    fi, err := f.Stat()
    if err != nil {
        log.Println(err)
        return
    }
    bar := uiprogress.AddBar(int(fi.Size()))
    bar.AppendCompleted()
    bar.PrependElapsed()

    r := &barReader{f, bar}

    _, err = io.Copy(conn, r)
    if err != nil {
        log.Println("Unable to copy file:", err)
        return
    }
}

type barReader struct {
    f *os.File
    b *uiprogress.Bar
}

func (r *barReader) Read(buf []byte) (int, error) {
    n, err := r.f.Read(buf)
    r.b.Set(r.b.Current() + n)
    return n, err
}
