package internal

import (
	"container/list"
	"fmt"
	set "github.com/deckarep/golang-set"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func createRootFile(rootA *FileNode) func() {
	file, err := os.OpenFile("test.txt", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
	if err != nil {
		fmt.Println("Create error, ", err.Error())
	}
	queue := list.New()
	queue.PushBack(rootA)
	for queue.Len() != 0 {
		cur := queue.Front()
		queue.Remove(cur)
		node, _ := cur.Value.(*FileNode)
		_, _ = file.WriteString(node.String())
		for _, child := range node.ChildNodes {
			queue.PushBack(child)
		}
	}
	_ = file.Close()
	return func() {
		_ = os.Remove(file.Name())
	}
}

//func TestReadRootLines(t *testing.T) {
//	test := map[string]*struct {
//		expectRoot *FileNode
//	}{
//		"RootA": {
//			expectRoot: GetRootA(),
//		},
//	}
//
//	for n, c := range test {
//		t.Run(n, func(t *testing.T) {
//			teardown := createRootFile(c.expectRoot)
//			defer teardown()
//			mapp := ReadRootLines("test.txt")
//			_, ok := mapp[c.expectRoot.Id]
//			assert.True(t, ok)
//		})
//	}
//}
//
//func TestRootDeserialize(t *testing.T) {
//	rootA := GetRootA()
//	teardown := createRootFile(rootA)
//	defer teardown()
//	nodeMap := ReadRootLines("test.txt")
//	rootB := RootDeserialize(nodeMap)
//	assert.True(t, rootA.IsDeepEqualTo(rootB))
//}

func TestWrite(t *testing.T) {
	file, err := os.OpenFile("write.txt", os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		fmt.Println(err)
	}
	defer file.Close()
	var wg = &sync.WaitGroup{}
	var word = "HelloWorld\n"
	for i := 0; i < 10; i++ {
		wg.Add(1)
		p := i
		go func(index int) {
			defer wg.Done()
			content := fmt.Sprintf("%v%s", index, word)
			fmt.Printf("Write %d chunk,Content %s", index, content)
			_, err = file.Seek(int64(len(content)*index), 0)
			if err != nil {
				fmt.Println(err)
			}
			_, err = file.WriteString(content)
			if err != nil {
				fmt.Println(err)
			}
		}(p)
	}
	wg.Wait()
	fmt.Println("Done")
}

func TestRead(t *testing.T) {
	file, _ := os.Open("write.txt")
	for i := 0; i < 7; i++ {
		bytes := make([]byte, 119)
		n, err := file.Read(bytes)
		if err == io.EOF {
			fmt.Println(err)
		}
		fmt.Println(string(bytes[:n]))
	}

}

func TestHeartbeatOperation_Apply(t *testing.T) {
	node := &DataNode{
		Id:            "aaaa",
		status:        0,
		Address:       "localhost:9090",
		Chunks:        set.NewSet(),
		Leases:        set.NewSet(),
		HeartbeatTime: time.Time{},
	}
	AddDataNode(node)
	a := &HeartbeatOperation{
		Id:         "op1",
		DataNodeId: "aaaa",
		ChunkIds:   []string{"a", "b", "c"},
	}
	b := &HeartbeatOperation{
		Id:         "op2",
		DataNodeId: "aaaa",
		ChunkIds:   []string{"a", "b", "d"},
	}
	c := &HeartbeatOperation{
		Id:         "op3",
		DataNodeId: "aaaa",
		ChunkIds:   []string{"a", "d", "c", "f"},
	}
	f := func(operation *HeartbeatOperation) {
		operation.Apply()
	}
	go f(a)
	go f(b)
	go f(c)
	time.Sleep(time.Second)
	println(node.Chunks.String())
}
