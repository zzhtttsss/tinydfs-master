package internal

import (
	"bufio"
	"fmt"
	set "github.com/deckarep/golang-set"
	"github.com/hashicorp/raft"
	"github.com/spf13/viper"
	"go.uber.org/atomic"
	"math"
	"strings"
	"sync"
	"time"
	"tinydfs-base/common"
	"tinydfs-base/util"
)

const (
	chunkIdIdx = iota
	dataNodesIdx
	pendingDataNodesIdx
)

var (
	// chunksMap stores all Chunk in the file system, using id as the key.
	chunksMap        = make(map[string]*Chunk)
	updateChunksLock = &sync.RWMutex{}
	// pendingChunkQueue stores all Chunk that are missing a replica and waiting
	// to be allocated to a DataNode.
	pendingChunkQueue = util.NewQueue[String]()
)

type Chunk struct {
	// Id is FileNodeId+_+ChunkNum
	Id string
	// dataNodes includes all id of DataNode which are storing this Chunk.
	dataNodes set.Set
	// pendingDataNodes includes all id of DataNode which will store this Chunk.
	// It means these DataNode is already allocated to store this Chunk, but they
	// have not truly store this Chunk in their hard drive.
	pendingDataNodes set.Set
}

func (c *Chunk) String() string {
	res := strings.Builder{}
	dataNodes := make([]string, c.dataNodes.Cardinality())
	dataNodeChan := c.dataNodes.Iter()
	index := 0
	for dataNodeId := range dataNodeChan {
		dataNodes[index] = dataNodeId.(string)
		index++
	}

	pendingDataNodes := make([]string, c.pendingDataNodes.Cardinality())
	pendingDataNodeChan := c.pendingDataNodes.Iter()
	index = 0
	for dataNodeId := range pendingDataNodeChan {
		pendingDataNodes[index] = dataNodeId.(string)
		index++
	}

	res.WriteString(fmt.Sprintf("%s$%v$%v\n",
		c.Id, dataNodes, pendingDataNodes))
	return res.String()
}

func AddChunk(chunk *Chunk) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	chunksMap[chunk.Id] = chunk
}

func BatchAddChunk(chunks []*Chunk) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	for _, chunk := range chunks {
		chunksMap[chunk.Id] = chunk
	}
}

func GetChunk(id string) *Chunk {
	updateChunksLock.RLock()
	defer updateChunksLock.RUnlock()
	return chunksMap[id]
}

func BatchClearDataNode(chunkIds []interface{}, dataNodeId string) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	for _, id := range chunkIds {
		if chunk, ok := chunksMap[id.(string)]; ok {
			chunk.dataNodes.Remove(dataNodeId)
		}
	}
}

// BatchClearPendingDataNodes clear all pendingDataNodes of Chunk's id in the
// given slice.
func BatchClearPendingDataNodes(chunkIds []string) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	for _, id := range chunkIds {
		if chunk, ok := chunksMap[id]; ok {
			chunk.pendingDataNodes.Clear()
			pendingChunkQueue.Push(String(id))
		}
	}
}

// BatchUpdatePendingDataNodes move all DataNode which have store the corresponding
// Chunk successfully from that Chunk's pendingDataNodes to its dataNodes.
func BatchUpdatePendingDataNodes(infos []util.ChunkTaskResult) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	for _, info := range infos {
		if chunk, ok := chunksMap[info.ChunkId]; ok {
			for _, id := range info.SuccessDataNodes {
				chunk.dataNodes.Add(id)
			}
			for i := 0; i < len(info.FailDataNodes); i++ {
				pendingChunkQueue.Push(String(info.ChunkId))
			}
			chunk.pendingDataNodes.Clear()
		}
	}
}

// BatchFilterChunk filter Chunk that still exists, and it's DataNode is not full
// from given Chunk's id slice.
func BatchFilterChunk(ids []string) []string {
	updateChunksLock.RLock()
	defer updateChunksLock.RUnlock()
	chunkIds := make([]string, 0, len(ids))
	for i := 0; i < len(ids); i++ {
		// Chunk should still exist, and it's DataNode is not full.
		if chunk, ok := chunksMap[ids[i]]; ok {
			if chunk.dataNodes.Cardinality()+chunk.pendingDataNodes.Cardinality() < viper.GetInt(common.ReplicaNum) {
				chunkIds = append(chunkIds, ids[i])
			}
		}
	}
	return chunkIds
}

// BatchApplyPlan2Chunk use the given plan to allocate DataNode for each Chunk.
func BatchApplyPlan2Chunk(plan []int, chunkIds []string, dataNodeIds []string) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	for i, dnIndex := range plan {
		chunksMap[chunkIds[i]].pendingDataNodes.Add(dataNodeIds[dnIndex])
	}
}

// PersistChunks writes all Chunk in chunksMap to the sink for persistence.
func PersistChunks(sink raft.SnapshotSink) error {
	for _, chunk := range chunksMap {
		_, err := sink.Write([]byte(chunk.String()))
		if err != nil {
			return err
		}
	}
	_, err := sink.Write([]byte(common.SnapshotDelimiter))
	if err != nil {
		return err
	}
	return nil
}

// RestoreChunks reads all Chunk from the buf and puts them into chunksMap.
func RestoreChunks(buf *bufio.Scanner) error {
	dataNodes := set.NewSet()
	pendingDataNodes := set.NewSet()
	chunksMap = map[string]*Chunk{}
	for buf.Scan() {
		line := buf.Text()
		if line == common.SnapshotDelimiter {
			return nil
		}
		data := strings.Split(line, "$")

		dataNodesLen := len(data[dataNodesIdx])
		dataNodesData := data[dataNodesIdx][1 : dataNodesLen-1]
		for _, dnId := range strings.Split(dataNodesData, " ") {
			dataNodes.Add(dnId)
		}
		pendingDataNodesLen := len(data[pendingDataNodesIdx])
		pendingDataNodesData := data[pendingDataNodesIdx][1 : pendingDataNodesLen-1]
		for _, dnId := range strings.Split(pendingDataNodesData, " ") {
			pendingDataNodes.Add(dnId)
		}
		chunksMap[data[chunkIdIdx]] = &Chunk{
			Id:               data[chunkIdIdx],
			dataNodes:        dataNodes,
			pendingDataNodes: pendingDataNodes,
		}
	}
	return nil
}

type String string

func (s String) String() string {
	return string(s)
}

type PendingChunkQueue struct {
	queue *util.Queue[String]
	state *atomic.Uint32
}

func (q *PendingChunkQueue) String() string {
	return q.queue.String()
}

func PersistPendingChunkQueue(sink raft.SnapshotSink) error {
	_, err := sink.Write([]byte(pendingChunkQueue.String()))
	if err != nil {
		return err
	}

	_, err = sink.Write([]byte(common.SnapshotDelimiter))
	if err != nil {
		return err
	}
	return nil
}

func RestorePendingChunkQueue(buf *bufio.Scanner) error {
	for buf.Scan() {
		line := buf.Text()
		if line == common.SnapshotDelimiter {
			return nil
		}
		line = strings.Trim(line, common.DollarDelimiter)
		data := strings.Split(line, "$")
		for _, datum := range data {
			pendingChunkQueue.Push(String(datum))
		}
	}
	return nil
}

// BatchAllocateChunks runs in a goroutine. It will get a batch of Chunk from
// pendingChunkQueue and the best plan which allocate a target DataNode to
// store for each Chunk.
// 1. Get batch of Chunk from pendingChunkQueue.
// 2. Filter legal Chunk and alive DataNode.
// 3. Get current store state(which Chunk is stored by which DataNode).
// 4. Use DFS algorithm to get the best plan which decide the receiver and sender
//    of every Chunk to make the number of Chunk received and send by each DataNode
//    as balanced as possible(use variance to measure).
func BatchAllocateChunks() {
	Logger.Infof("Start to allocate a batch of chunks.")
	if pendingChunkQueue.Len() != 0 {
		batchChunkIds := getPendingChunks()
		chunkIds := BatchFilterChunk(batchChunkIds)
		dataNodeIds := GetAliveDataNodeIds()
		isStore := getStoreState(chunkIds, dataNodeIds)
		// Todo DataNode num is less than replicate num or other similar situation so that a Chunk can not find a DataNode to store.
		receiverPlan := allocateChunksDFS(len(chunkIds), len(dataNodeIds), isStore)
		for i := 0; i < len(isStore); i++ {
			for j := 0; j < len(isStore[0]); j++ {
				isStore[i][j] = !isStore[i][j]
			}
		}
		senderPlan := allocateChunksDFS(len(chunkIds), len(dataNodeIds), isStore)
		Logger.Debugf("Receiver plan is %v", receiverPlan)
		Logger.Debugf("Sender plan is %v", senderPlan)
		operation := &AllocateChunksOperation{
			Id:           util.GenerateUUIDString(),
			SenderPlan:   senderPlan,
			ReceiverPlan: receiverPlan,
			ChunkIds:     chunkIds,
			DataNodeIds:  dataNodeIds,
			BatchLen:     len(batchChunkIds),
		}
		data := getData4Apply(operation, common.OperationAllocateChunks)
		applyFuture := GlobalMasterHandler.Raft.Apply(data, 5*time.Second)
		if err := applyFuture.Error(); err != nil {
			Logger.Errorf("Fail to allocate a batch of chunks, error detail: %s,", err.Error())
		}
	}
	Logger.Infof("Suceess to allocate a batch of chunks.")
}

// ApplyAllocatePlan will apply the given allocating plan. It will:
// 1. Apply the best plan to all target Chunk.
// 2. Apply the best plan to all target DataNode.
// 3. Remove the batch of Chunk from pendingChunkQueue.
func ApplyAllocatePlan(senderPlan []int, receiverPlan []int, chunkIds []string, dataNodeIds []string,
	batchLen int) {
	BatchApplyPlan2Chunk(receiverPlan, chunkIds, dataNodeIds)
	BatchApplyPlan2DataNode(receiverPlan, senderPlan, chunkIds, dataNodeIds)
	pendingChunkQueue.BatchPop(batchLen)
}

// getPendingChunks get a batch of Chunk's id from the pendingChunkQueue. The
// batch size is the minimum of the current len of the pendingChunkQueue and
// the maximum size.
func getPendingChunks() []string {
	var (
		maxCount  = viper.GetInt(common.ChunkDeadChunkCopyThreshold)
		copyCount int
	)
	if pendingChunkQueue.Len() > maxCount {
		copyCount = maxCount
	} else {
		copyCount = pendingChunkQueue.Len()
	}
	batchTs := pendingChunkQueue.BatchTop(copyCount)
	batchChunkIds := make([]string, len(batchTs))
	for i := 0; i < len(batchTs); i++ {
		batchChunkIds[i] = batchTs[i].String()
	}
	return batchChunkIds
}

// getStoreState gets the state of all DataNode which store target Chunk for all
// given Chunk. We need to check both pendingDataNodes and dataNodes of a Chunk.
func getStoreState(chunkIds []string, dataNodeIds []string) [][]bool {
	updateChunksLock.RLock()
	defer updateChunksLock.RUnlock()
	isStore := make([][]bool, len(chunkIds))
	for i := range isStore {
		isStore[i] = make([]bool, len(dataNodeIds))
	}
	dnIndexMap := make(map[string]int)
	for i, id := range dataNodeIds {
		dnIndexMap[id] = i
	}
	for i, id := range chunkIds {
		chunk := chunksMap[id]
		dataNodes := chunk.dataNodes.ToSlice()
		pendingDataNodes := chunk.pendingDataNodes.ToSlice()
		for _, dnId := range dataNodes {
			isStore[i][dnIndexMap[dnId.(string)]] = true
		}
		for _, pdnId := range pendingDataNodes {
			isStore[i][dnIndexMap[pdnId.(string)]] = true
		}
	}
	return isStore
}

// allocateChunksDFS calculate the best allocating plan base on the given information.
func allocateChunksDFS(chunkNum int, dataNodeNum int, isStore [][]bool) []int {
	currentResult := make([][]int, dataNodeNum)
	for i := range currentResult {
		currentResult[i] = make([]int, 0)
	}
	result := make([]int, chunkNum)
	minValue := math.MaxInt
	avg := int(math.Ceil(float64(chunkNum / dataNodeNum)))
	bestVariance := calBestVariance(chunkNum, dataNodeNum, avg)
	for i := 0; i < dataNodeNum; i++ {
		if dfs(chunkNum, dataNodeNum, 0, i, &currentResult, isStore, &result, &minValue, avg, bestVariance) {
			break
		}
	}
	return result
}

// calBestVariance calculate the minimum variance of current situation.
func calBestVariance(chunkNum int, dataNodeNum int, avg int) int {
	if avg*dataNodeNum == chunkNum {
		return 0
	}
	return chunkNum - (avg-1)*dataNodeNum
}

// dfs recursively find the best plan to make the allocation plan as uniform as
// possible(use variance to measure).
func dfs(chunkNum int, dataNodeNum int, chunkIndex int, dnIndex int, currentResult *[][]int,
	isStore [][]bool, result *[]int, minValue *int, avg int, bestVariance int) bool {
	if chunkIndex == chunkNum {
		currentValue := 0
		for i := 0; i < dataNodeNum; i++ {
			currentValue += int(math.Pow(float64(len((*currentResult)[i])-avg), 2))
		}
		if currentValue < *minValue {
			*minValue = currentValue
			for j := 0; j < dataNodeNum; j++ {
				for k := 0; k < len((*currentResult)[j]); k++ {
					(*result)[(*currentResult)[j][k]] = j
				}
			}

		}
		// If the best plan has been found， just stop dfs and return the result.
		if currentValue == bestVariance {
			return true
		}
		return false
	}
	(*currentResult)[dnIndex] = append((*currentResult)[dnIndex], chunkIndex)
	for i := 0; i < dataNodeNum; i++ {
		if isStore[chunkIndex][dnIndex] {
			continue
		}
		isStore[chunkIndex][dnIndex] = true
		isBest := dfs(chunkNum, dataNodeNum, chunkIndex+1, i, currentResult, isStore, result, minValue, avg, bestVariance)
		isStore[chunkIndex][dnIndex] = false
		if isBest {
			return isBest
		}
	}
	(*currentResult)[dnIndex] = (*currentResult)[dnIndex][:len((*currentResult)[dnIndex])-1]
	return false
}

// UpdateChunk4Heartbeat delete the corresponding DataNode in the pendingDataNodes of
// each Chunk according to the Chunk sending information given by the heartbeat.
func UpdateChunk4Heartbeat(o HeartbeatOperation) {
	updateChunksLock.Lock()
	defer updateChunksLock.Unlock()
	for _, info := range o.SuccessInfos {
		if chunk, ok := chunksMap[info.ChunkId]; ok {
			chunk.pendingDataNodes.Remove(info.DataNodeId)
			chunk.dataNodes.Add(info.DataNodeId)
			if info.SendType == common.MoveSendType {
				chunk.dataNodes.Remove(o.DataNodeId)
			}
		}
	}
	for _, info := range o.FailInfos {
		if chunk, ok := chunksMap[info.ChunkId]; ok {
			chunk.pendingDataNodes.Remove(info.DataNodeId)
		}
	}
	for _, chunkId := range o.InvalidChunks {
		if chunk, ok := chunksMap[chunkId]; ok {
			chunk.dataNodes.Remove(o.DataNodeId)
			pendingChunkQueue.Push(String(chunkId))
		}
	}
}
