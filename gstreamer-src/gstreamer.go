package gstreamer_src

// #cgo pkg-config: gstreamer-1.0 gstreamer-app-1.0
// #include "gstreamer.h"
import "C"
import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"rtp-audio-processor/sets"
	"sync"
	"time"
	"unsafe"
)

type NotFoundError struct {
	Text string
}

func (e *NotFoundError) Error() string {
	return e.Text
}

func NewPipelineNotFoundError(pipelineId string) *NotFoundError {
	return &NotFoundError{Text: fmt.Sprintf("Pipeline(id=%v)", pipelineId)}
}

func NewEndpointNotFoundError(endpoint string) *NotFoundError {
	return &NotFoundError{Text: fmt.Sprintf("Endpoint(id=%v)", endpoint)}
}

type knownEndpointInfo struct {
	audioMixerSinkPad *C.GstPad
	ringBuffer        *C.RingBuffer
}

type unknownEndpointInfo struct {
	audioMixerSinkPad *C.GstPad
	appSink           *C.GstElement
}

type pipelineType struct {
	videobridgeId              string
	confGid                    string
	pipeline                   *C.PipelineData
	srcPort                    int
	touchTime                  time.Time
	ssrcEndpointMap            map[int]string
	speakers                   sets.StringSet
	endpointInfoMap            map[string]knownEndpointInfo
	unknownSsrcEndpointInfoMap map[int]unknownEndpointInfo
	lock                       sync.Mutex
}

type exportType struct {
	buf  *bytes.Buffer
	done chan struct{}
}

func (p *pipelineType) expired() bool {
	return time.Since(p.touchTime).Minutes() > 1
}

var pipelines map[string]*pipelineType
var pipelinesMutex sync.Mutex
var exports map[uint64]exportType
var exportsMutex sync.Mutex

func init() {
	exports = make(map[uint64]exportType)
	pipelines = make(map[string]*pipelineType)
	go func() {
		for range time.Tick(time.Minute) {
			expirePipelines()
		}
	}()

	C.gstreamer_init()
	go C.gstreamer_send_start_mainloop()
}

func expirePipelines() {
	pipelinesMutex.Lock()
	defer pipelinesMutex.Unlock()

	for pipelineId, pipeline := range pipelines {
		if pipeline.expired() {
			C.gstreamer_delete_pipeline(pipeline.pipeline)
			delete(pipelines, pipelineId)
		}
	}
}

func CreatePipeline(id, sinkHost string, sinkPort, seqNum int) (int, bool, error) {
	pipelinesMutex.Lock()
	defer pipelinesMutex.Unlock()

	if pipeline, pipelineExists := pipelines[id]; pipelineExists {
		pipeline.touchTime = time.Now()
		return pipeline.srcPort, false, nil
	}

	idUnsafe := C.CString(id)
	defer C.free(unsafe.Pointer(idUnsafe))

	sinkHostUnsafe := C.CString(sinkHost)
	defer C.free(unsafe.Pointer(sinkHostUnsafe))

	var srcPort C.gint
	pipeline := C.gstreamer_create_pipeline(idUnsafe, sinkHostUnsafe, C.gint(sinkPort), C.guint(seqNum), &srcPort)
	pipelines[id] = &pipelineType{
		pipeline:                   pipeline,
		srcPort:                    int(srcPort),
		touchTime:                  time.Now(),
		ssrcEndpointMap:            map[int]string{},
		speakers:                   sets.NewStringSet(),
		endpointInfoMap:            map[string]knownEndpointInfo{},
		unknownSsrcEndpointInfoMap: map[int]unknownEndpointInfo{},
	}
	return int(srcPort), true, nil
}

func UpdatePipeline(id string, ssrcEndpointMap map[int]string, speakers [] /*endpointId*/ string) error {
	pipeline, ok := pipelines[id]
	if !ok {
		return NewPipelineNotFoundError(id)
	}

	pipeline.lock.Lock()
	defer pipeline.lock.Unlock()

	if ssrcEndpointMap != nil {
		for ssrc, endpointId := range ssrcEndpointMap {
			pipeline.ssrcEndpointMap[ssrc] = endpointId
			if endpointInfo, ok := pipeline.unknownSsrcEndpointInfoMap[ssrc]; ok {
				pipeline.endpointInfoMap[endpointId] = knownEndpointInfo{
					audioMixerSinkPad: endpointInfo.audioMixerSinkPad,
					ringBuffer:        C.linkAndUnrefAppSink(endpointInfo.appSink, nil),
				}
				delete(pipeline.unknownSsrcEndpointInfoMap, ssrc)
			}
		}
	}

	if speakers != nil {
		pipeline.speakers = sets.NewStringSetFromSlice(speakers)
		for endpointId, endpointInfo := range pipeline.endpointInfoMap {
			mute := !pipeline.speakers.Contains(endpointId)
			C.setMuteProp(endpointInfo.audioMixerSinkPad, C.gboolean(boolToInt(mute)))
		}
	}

	return nil
}

func ExportPipeline(ctx context.Context, id, endpointId string) (*bytes.Buffer, error) {
	pipeline, ok := pipelines[id]
	if !ok {
		return nil, NewPipelineNotFoundError(id)
	}
	endpointInfo, ok := pipeline.endpointInfoMap[endpointId]
	if !ok {
		return nil, NewEndpointNotFoundError(endpointId)
	}

	exportsMutex.Lock()

	var contextId uint64
	for {
		contextId = rand.Uint64()
		if _, ok := exports[contextId]; !ok {
			break
		}
	}

	export := exportType{
		buf:  bytes.NewBuffer(make([]byte, 0, 48000*16*60*5/8 /*buffer for 5 minutes 48khz S16LE */)),
		done: make(chan struct{}),
	}

	exports[contextId] = export

	exportsMutex.Unlock()

	go C.ringbuffer_export(endpointInfo.ringBuffer, C.guint64(contextId))

	fmt.Printf("%v export started\n", contextId)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-export.done:
		return export.buf, nil
	}
}

func DeletePipeline(id string) error {
	pipelinesMutex.Lock()
	defer pipelinesMutex.Unlock()

	pipeline, ok := pipelines[id]
	if !ok {
		return NewPipelineNotFoundError(id)
	}
	for endpointId, endpointInfo := range pipeline.endpointInfoMap {
		C.gst_object_unref(C.gpointer(endpointInfo.audioMixerSinkPad))
		C.ringbuffer_free(endpointInfo.ringBuffer)
		delete(pipeline.endpointInfoMap, endpointId)
	}
	for ssrc, endpointInfo := range pipeline.unknownSsrcEndpointInfoMap {
		C.gst_object_unref(C.gpointer(endpointInfo.audioMixerSinkPad))
		C.gst_object_unref(C.gpointer(endpointInfo.appSink))
		delete(pipeline.unknownSsrcEndpointInfoMap, ssrc)
	}
	C.gstreamer_delete_pipeline(pipeline.pipeline)
	delete(pipelines, id)
	return nil
}

//export goHandleBuffer
func goHandleBuffer(contextId C.guint64, buffer unsafe.Pointer, bufferLen C.int) {
	exportsMutex.Lock()
	defer exportsMutex.Unlock()

	if export, ok := exports[uint64(contextId)]; ok {
		export.buf.Write(C.GoBytes(buffer, bufferLen))
	}
}

//export goHandleBufferEnd
func goHandleBufferEnd(contextId C.guint64) {
	exportsMutex.Lock()
	defer exportsMutex.Unlock()

	if export, ok := exports[uint64(contextId)]; ok {
		close(export.done)
		fmt.Printf("%v export finished\n", uint64(contextId))
		delete(exports, uint64(contextId))
	}
}

//export goOnNewSsrc
func goOnNewSsrc(pipelineId *C.gchar, ssrc C.guint, appsink *C.GstElement, audioMixerSinkPad *C.GstPad) {
	if pipeline, ok := pipelines[C.GoString(pipelineId)]; ok {
		pipeline.lock.Lock()
		defer pipeline.lock.Unlock()

		if endpointId, ok := pipeline.ssrcEndpointMap[int(ssrc)]; ok {
			var ringBuffer *C.RingBuffer
			if oldEndpointInfo, ok := pipeline.endpointInfoMap[endpointId]; ok {
				// reconnect
				ringBuffer = C.linkAndUnrefAppSink(appsink, oldEndpointInfo.ringBuffer)
				C.setMuteProp(oldEndpointInfo.audioMixerSinkPad, C.TRUE)
				C.gst_object_unref(C.gpointer(oldEndpointInfo.audioMixerSinkPad))
			} else {
				ringBuffer = C.linkAndUnrefAppSink(appsink, nil)
			}
			pipeline.endpointInfoMap[endpointId] = knownEndpointInfo{
				audioMixerSinkPad: audioMixerSinkPad,
				ringBuffer:        ringBuffer,
			}
			if pipeline.speakers.Contains(endpointId) {
				C.setMuteProp(audioMixerSinkPad, C.FALSE)
			}
		} else {
			pipeline.unknownSsrcEndpointInfoMap[int(ssrc)] = unknownEndpointInfo{
				audioMixerSinkPad: audioMixerSinkPad,
				appSink:           appsink,
			}
		}
	} else {
		fmt.Printf("Unknown pipeline(id=%v)\n", C.GoString(pipelineId))
	}
}
