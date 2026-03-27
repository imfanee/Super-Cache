package main

func main() {
	var ch chan int
	select {
	case ch <- 1:
		println("sent")
	default:
		println("default")
	}
}
