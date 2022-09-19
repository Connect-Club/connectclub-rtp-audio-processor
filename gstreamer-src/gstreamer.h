#ifndef GST_H
#define GST_H

#include <glib.h>
#include <gst/gst.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct _RingBuffer RingBuffer;
typedef struct _PipelineData PipelineData;

extern void goOnNewSsrc(gchar *pipelineId, guint ssrc, GstElement* appsink, GstPad* audioMixerSinkPad);
extern void goHandleBuffer(guint64 contextId, void *buffer, int bufferLen);
extern void goHandleBufferEnd(guint64 contextId);

void gstreamer_init(void);
PipelineData* gstreamer_create_pipeline(gchar *id, gchar *sink_host, gint sink_port, guint seqnum, gint *src_port);
void gstreamer_delete_pipeline(PipelineData *pipeline);
void gstreamer_send_start_mainloop(void);

RingBuffer* linkAndUnrefAppSink(GstElement* appsink, RingBuffer* ringBuffer);
void ringbuffer_export(RingBuffer * ringBuffer, guint64 contextId);
void ringbuffer_free(RingBuffer * ringBuffer);

void setMuteProp(GstPad* audioMixerSinkPad, gboolean mute);

#endif