package limiter

import (
	"container/heap"
	"time"
)

type bucketQueue struct {
	region    string
	bucket    string
	high      []*admitRequest
	normal    []*admitRequest
	wakeAt    time.Time
	heapIndex int
}

func (b *bucketQueue) depth() int {
	return len(b.high) + len(b.normal)
}

func (b *bucketQueue) enqueue(req *admitRequest) {
	if req.admission.Priority == PriorityHigh {
		b.high = append(b.high, req)
		return
	}
	b.normal = append(b.normal, req)
}

func (b *bucketQueue) dequeueValid() *admitRequest {
	for len(b.high) > 0 {
		req := b.high[0]
		b.high[0] = nil
		b.high = b.high[1:]
		if req.ctx.Err() == nil {
			return req
		}
	}

	for len(b.normal) > 0 {
		req := b.normal[0]
		b.normal[0] = nil
		b.normal = b.normal[1:]
		if req.ctx.Err() == nil {
			return req
		}
	}

	return nil
}

type wakeHeap []*bucketQueue

func (h wakeHeap) Len() int { return len(h) }

func (h wakeHeap) Less(i, j int) bool { return h[i].wakeAt.Before(h[j].wakeAt) }

func (h wakeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *wakeHeap) Push(x any) {
	item := x.(*bucketQueue)
	item.heapIndex = len(*h)
	*h = append(*h, item)
}

func (h *wakeHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	item.heapIndex = -1
	*h = old[:last]
	return item
}

func upsertWake(h *wakeHeap, bucket *bucketQueue, at time.Time) {
	if at.IsZero() {
		removeWake(h, bucket)
		return
	}

	bucket.wakeAt = at
	if bucket.heapIndex >= 0 {
		heap.Fix(h, bucket.heapIndex)
		return
	}
	heap.Push(h, bucket)
}

func removeWake(h *wakeHeap, bucket *bucketQueue) {
	if bucket.heapIndex < 0 {
		bucket.wakeAt = time.Time{}
		return
	}
	heap.Remove(h, bucket.heapIndex)
	bucket.wakeAt = time.Time{}
}
