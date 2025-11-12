package main
import (
  "log"
  "net"
)
func main(){
  l, err := net.Listen("unix", "/Users/hhx/work/upwork/tmux_coder/test.sock")
  if err != nil { log.Fatal(err) }
  defer l.Close()
}
