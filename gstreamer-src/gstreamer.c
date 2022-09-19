#include "gstreamer.h"

GMainLoop *gstreamer_send_main_loop = NULL;
void gstreamer_send_start_mainloop(void) {
  gst_init(NULL, NULL);

  gstreamer_send_main_loop = g_main_loop_new(NULL, FALSE);

  g_main_loop_run(gstreamer_send_main_loop);
}

void gstreamer_init(void) {
  /* Initialize GStreamer */
  gst_init (NULL, NULL);
}

/* Structure to contain all our information, so we can pass it to callbacks */
typedef struct _PipelineData{
  GstElement *pipeline;
  GstElement *audiomixer;
  GstPadTemplate *audiomixerSinkPadTemplate;
} PipelineData;

typedef struct _RingBufferItem{
  gpointer content;
  gsize size;
  GstClockTime duration;
  struct _RingBufferItem * next;
  struct _RingBufferItem * prev;
} RingBufferItem;

typedef struct _RingBuffer{
  gchar * endpoint;
  gsize itemContentCapacity;
  RingBufferItem * firstItem;
  RingBufferItem * lastItem;
  GstClockTime curDuration;
  GstClockTime maxDuration;
  GMutex lock;
} RingBuffer;

static void pad_added_handler (GstElement *demux, guint ssrc, GstPad *pad, PipelineData *data);
static void pad_removed_handler (GstElement *demux, guint ssrc, GstPad *pad, PipelineData *data);

static gboolean gstreamer_send_bus_call(GstBus *bus, GstMessage *msg, gpointer user_data);

static GstFlowReturn gstreamer_send_new_sample_handler(GstElement *object, gpointer user_data);

PipelineData* gstreamer_create_pipeline(gchar *id, gchar *sink_host, gint sink_port, guint seqnum, gint *src_port) {
  g_print ("%s. Start pipeline(sinkPort=%d).\n", id, sink_port);

  PipelineData *data = calloc(1, sizeof(PipelineData));
  GstMessage *msg;
  GstStateChangeReturn ret;
  gboolean terminate = FALSE;

  /* Create the elements */
  GstElement *udpsrc = gst_element_factory_make("udpsrc", NULL);
  GstElement *rtpssrcdemux = gst_element_factory_make("rtpssrcdemux", NULL);
  data->audiomixer = gst_element_factory_make("audiomixer", NULL);
  data->audiomixerSinkPadTemplate = gst_element_class_get_pad_template(GST_ELEMENT_GET_CLASS(data->audiomixer), "sink_%u");
  GstElement *opusenc = gst_element_factory_make("opusenc", NULL);
  GstElement *rtpopuspay = gst_element_factory_make("rtpopuspay", NULL);
  GstElement *rtpsession = gst_element_factory_make("rtpsession", NULL);
  GstElement *rtp_udpsink = gst_element_factory_make("udpsink", NULL);
  GstElement *rtcp_udpsink = gst_element_factory_make("udpsink", NULL);

  /* Create the empty pipeline */
  data->pipeline = gst_pipeline_new(id);

  if (!data->pipeline || !udpsrc || !rtpssrcdemux || !data->audiomixer || !opusenc || !rtpopuspay || !rtpsession || !rtp_udpsink || !rtcp_udpsink) {
    g_printerr ("%s. Not all elements could be created.\n", id);
    return NULL;
  }

  GstCaps *udpsrc_caps = gst_caps_new_simple ("application/x-rtp",
               "media", G_TYPE_STRING, "audio",
               "clock-rate", G_TYPE_INT, 48000,
               "encoding-name", G_TYPE_STRING, "OPUS",
               "payload", G_TYPE_INT, 111,
               NULL);
  g_object_set (udpsrc, "port", 0, "caps", udpsrc_caps, NULL);
  g_object_set (rtpopuspay, "pt", 111, "seqnum-offset", seqnum, NULL);
  g_object_set (rtp_udpsink, "host", sink_host, "port", sink_port, NULL);
  g_object_set (rtcp_udpsink, "host", sink_host, "port", sink_port, NULL);

  /* Build the pipeline. Note that we are NOT linking the source at this
   * point. We will do it later. */
  gst_bin_add_many (GST_BIN (data->pipeline), udpsrc, rtpssrcdemux, data->audiomixer, opusenc, rtpopuspay, rtpsession, rtp_udpsink, rtcp_udpsink,  NULL);

  if (!gst_element_link_many (udpsrc, rtpssrcdemux, NULL)) {
    g_printerr ("%s. Elements could not be linked.\n", id);
    gst_object_unref (data->pipeline);
    return NULL;
  }

  if (!gst_element_link_many (data->audiomixer, opusenc, rtpopuspay, NULL)) {
    g_printerr ("%s. Elements could not be linked.\n", id);
    gst_object_unref (data->pipeline);
    return NULL;
  }

  GstPad *rtpopuspay_src_pad = gst_element_get_static_pad (rtpopuspay, "src");
  GstPad *send_rtp_sink = gst_element_get_request_pad(rtpsession, "send_rtp_sink");
  GstPadLinkReturn link_ret = gst_pad_link (rtpopuspay_src_pad, send_rtp_sink);
  if (GST_PAD_LINK_FAILED (link_ret)) {
    g_print ("%s. Link failed (rtpopuspay-rtpsession).\n", id);
  } else {
    g_print ("%s. Link succeeded (rtpopuspay-rtpsession).\n", id);
  }

  GstPad *rtp_udpsink_pad = gst_element_get_static_pad (rtp_udpsink, "sink");
  GstPad *send_rtp_src = gst_element_get_static_pad (rtpsession, "send_rtp_src");
  link_ret = gst_pad_link (send_rtp_src, rtp_udpsink_pad);
  if (GST_PAD_LINK_FAILED (link_ret)) {
    g_print ("%s. Link failed (rtpsession.send_rtp_src-udpsink).\n", id);
  } else {
    g_print ("%s. Link succeeded (rtpsession.send_rtp_src-udpsink).\n", id);
  }

  GstPad *rtcp_udpsink_pad = gst_element_get_static_pad (rtcp_udpsink, "sink");
  GstPad *send_rtcp_src = gst_element_get_request_pad (rtpsession, "send_rtcp_src");
  link_ret = gst_pad_link (send_rtcp_src, rtcp_udpsink_pad);
  if (GST_PAD_LINK_FAILED (link_ret)) {
    g_print ("%s. Link failed (rtpsession.send_rtcp_src-udpsink).\n", id);
  } else {
    g_print ("%s. Link succeeded (rtpsession.send_rtcp_src-udpsink).\n", id);
  }

  /* Connect to the pad-added signal */
  g_signal_connect (rtpssrcdemux, "new-ssrc-pad", G_CALLBACK (pad_added_handler), data);
  g_signal_connect (rtpssrcdemux, "removed-ssrc-pad", G_CALLBACK (pad_removed_handler), data);

  /* Listen to the bus */
  GstBus *bus = gst_element_get_bus (data->pipeline);
  gst_bus_add_watch(bus, gstreamer_send_bus_call, data);
  gst_object_unref(bus);

  /* Start playing */
  ret = gst_element_set_state (data->pipeline, GST_STATE_PLAYING);
  if (ret == GST_STATE_CHANGE_FAILURE) {
    g_printerr ("%s. Unable to set the pipeline to the playing state.\n", id);
    gst_object_unref (data->pipeline);
    return NULL;
  }

  g_object_get(udpsrc, "port", src_port, NULL);
  return data;
}

void gstreamer_delete_pipeline(PipelineData *pipelineData) {
  GstStateChangeReturn result = gst_element_set_state (pipelineData->pipeline, GST_STATE_NULL);
  gst_object_unref (pipelineData->pipeline);
  free(pipelineData);
}

static gboolean gstreamer_send_bus_call(GstBus *bus, GstMessage *msg, gpointer user_data) {
    GError *err;
    gchar *debug_info;
    PipelineData *data = (PipelineData *)user_data;
    switch (GST_MESSAGE_TYPE (msg)) {
        case GST_MESSAGE_ERROR:
          gst_message_parse_error (msg, &err, &debug_info);
          g_printerr ("%s. Error received from element %s: %s\n", GST_OBJECT_NAME(data->pipeline), GST_OBJECT_NAME (msg->src), err->message);
          g_printerr ("%s. Debugging information: %s\n", GST_OBJECT_NAME(data->pipeline), debug_info ? debug_info : "none");
          g_clear_error (&err);
          g_free (debug_info);
          break;
        case GST_MESSAGE_EOS:
          g_print ("%s. End-Of-Stream reached.\n", GST_OBJECT_NAME(data->pipeline));
          break;
        case GST_MESSAGE_STATE_CHANGED:
          /* We are only interested in state-changed messages from the pipeline */
          if (GST_MESSAGE_SRC (msg) == GST_OBJECT (data->pipeline)) {
            GstState old_state, new_state, pending_state;
            gst_message_parse_state_changed (msg, &old_state, &new_state, &pending_state);
            g_print ("%s. Pipeline state changed from %s to %s. Pending state %s\n",
                GST_OBJECT_NAME(data->pipeline),
                gst_element_state_get_name (old_state),
                gst_element_state_get_name (new_state),
                gst_element_state_get_name (pending_state)
            );
          }
          break;
        default:
          //g_print("%s. Message received(%s) from %s.\n", GST_OBJECT_NAME(data->pipeline), GST_MESSAGE_TYPE_NAME(msg), GST_OBJECT_NAME(GST_MESSAGE_SRC (msg)));
          //g_printerr ("Unexpected message received.\n");
          break;
    }
    return TRUE;
}

/* This function will be called by the pad-added signal */
static void pad_added_handler (GstElement *demux, guint ssrc, GstPad *ssrc_src_pad, PipelineData *data) {
  g_print ("%s. Received new ssrc pad '%s' ssrc=%d from '%s':\n", GST_OBJECT_NAME(data->pipeline), GST_PAD_NAME (ssrc_src_pad), ssrc, GST_ELEMENT_NAME (demux));

  GError *error = NULL;
  GstElement *bin = gst_parse_bin_from_description(
    "rtpjitterbuffer ! rtpopusdepay ! audio/x-opus,rate=48000,channels=1,channel-mapping-family=0,stream-count=1,coupled-count=0 ! opusdec ! audio/x-raw,format=S16LE,channels=1 !"
      "tee name=t ! queue ! appsink name=appsink max-buffers=15000 drop=true "
      "t. ! queue",
    TRUE, &error
  );
  if (error != NULL) {
    g_print ("%s. Bin parse failed. Error: %s\n", GST_OBJECT_NAME(data->pipeline), error->message);
    gst_object_unref (data->pipeline);
    return;
  }

  if (!gst_bin_add(GST_BIN(data->pipeline), bin)) {
    g_print ("%s. Bin add failed.\n", GST_OBJECT_NAME(data->pipeline));
    gst_object_unref (data->pipeline);
    return;
  }

  GstPadLinkReturn ret;

  GstPad *bin_sink_pad = gst_element_get_static_pad (bin, "sink");
  ret = gst_pad_link (ssrc_src_pad, bin_sink_pad);
  gst_object_unref(bin_sink_pad);
  if (GST_PAD_LINK_FAILED (ret)) {
    g_print ("%s. Link failed (demux-bin).\n", GST_OBJECT_NAME(data->pipeline));
    gst_object_unref (data->pipeline);
    return;
  } else {
    g_print ("%s. Link succeeded (demux-bin).\n", GST_OBJECT_NAME(data->pipeline));
  }

  GstPad *bin_src_pad = gst_element_get_static_pad (bin, "src");
  GstPad *audiomixer_sink_pad = gst_element_get_request_pad(data->audiomixer, "sink_%u");
  g_object_set (audiomixer_sink_pad, "mute", TRUE, NULL);
  ret = gst_pad_link (bin_src_pad, audiomixer_sink_pad);
  gst_object_unref(bin_src_pad);
  if (GST_PAD_LINK_FAILED (ret)) {
    g_print ("%s. Link failed (bin-audiomixer).\n", GST_OBJECT_NAME(data->pipeline));
    gst_object_unref (data->pipeline);
    return;
  } else {
    g_print ("%s. Link succeeded (bin-audiomixer).\n", GST_OBJECT_NAME(data->pipeline));
  }

  GstElement *appsink = gst_bin_get_by_name(GST_BIN(bin), "appsink");
  goOnNewSsrc(GST_OBJECT_NAME(data->pipeline), ssrc, appsink, audiomixer_sink_pad);

  gst_element_set_state (bin, GST_STATE_PLAYING);
}

RingBuffer* linkAndUnrefAppSink(GstElement* appsink, RingBuffer* ringBuffer) {
  if (ringBuffer == NULL) {
    ringBuffer = calloc(1, sizeof(RingBuffer));
    ringBuffer->maxDuration = GST_SECOND*60*5;//store only last 5 minutes
    ringBuffer->itemContentCapacity = 48000*16/8;//buffer for 1 second 48khz S16LE mono
    g_mutex_init (&ringBuffer->lock);
  }
  g_object_set(appsink, "emit-signals", TRUE, NULL);
  g_signal_connect(appsink, "new-sample", G_CALLBACK(gstreamer_send_new_sample_handler), ringBuffer);
  gst_object_unref(appsink);
  g_print ("linkAndUnrefAppSink complete\n");
  return ringBuffer;
}

static void pad_removed_handler (GstElement *demux, guint ssrc, GstPad *ssrc_src_pad, PipelineData *data) {
  g_print ("%s. Received removed ssrc pad '%s' ssrc=%d from '%s':\n", GST_OBJECT_NAME(data->pipeline), GST_PAD_NAME (ssrc_src_pad), ssrc, GST_ELEMENT_NAME (demux));
}

static void ringbuffer_add(RingBuffer * ringBuffer, GstBuffer *gstBuf) {
  g_mutex_lock(&ringBuffer->lock);

  gsize bufSize = gst_buffer_get_size(gstBuf);

  if (ringBuffer->lastItem != NULL && (ringBuffer->lastItem->size+bufSize) <= ringBuffer->itemContentCapacity) {
    gst_buffer_extract(gstBuf, 0, ringBuffer->lastItem->content + ringBuffer->lastItem->size, bufSize);
    ringBuffer->lastItem->size += bufSize;
    ringBuffer->lastItem->duration += GST_BUFFER_DURATION(gstBuf);
    ringBuffer->curDuration += GST_BUFFER_DURATION(gstBuf);
  } else {
      RingBufferItem * newItem;
      if (ringBuffer->curDuration >= ringBuffer->maxDuration) {
        RingBufferItem * firstItem = ringBuffer->firstItem;
        ringBuffer->curDuration -= firstItem->duration;
        ringBuffer->firstItem = firstItem->next;
        if (ringBuffer->firstItem != NULL) {
          ringBuffer->firstItem->prev = NULL;
        }
        newItem = firstItem;
      } else {
        newItem = calloc(1, sizeof(RingBufferItem));
        newItem->content = malloc(ringBuffer->itemContentCapacity);
      }

      gst_buffer_extract(gstBuf, 0, newItem->content, bufSize);
      newItem->size = bufSize;
      newItem->duration = GST_BUFFER_DURATION(gstBuf);
      newItem->prev = ringBuffer->lastItem;
      newItem->next = NULL;
      if (ringBuffer->lastItem != NULL) {
        ringBuffer->lastItem->next = newItem;
      }
      ringBuffer->lastItem = newItem;
      if (ringBuffer->firstItem == NULL) {
        ringBuffer->firstItem = newItem;
      }
      ringBuffer->curDuration += newItem->duration;
  }

  g_mutex_unlock(&ringBuffer->lock);
}

static GstFlowReturn gstreamer_send_new_sample_handler(GstElement *object, gpointer user_data) {
  if (user_data == NULL) return GST_FLOW_OK;
  RingBuffer *ringBuffer = (RingBuffer *)user_data;

  GstSample *sample = NULL;
  g_signal_emit_by_name (object, "pull-sample", &sample);
  if (sample) {
    //g_print(gst_caps_to_string(gst_sample_get_caps(sample)));
    GstBuffer *buffer = gst_sample_get_buffer(sample);
    if (buffer) {
      ringbuffer_add(ringBuffer, buffer);
    }
    gst_sample_unref(sample);
  }

  return GST_FLOW_OK;
}

void ringbuffer_export(RingBuffer * ringBuffer, guint64 contextId) {
  g_mutex_lock(&ringBuffer->lock);

  RingBufferItem* item = ringBuffer->firstItem;
  while(item != NULL) {
    goHandleBuffer(contextId, item->content, item->size);
    item = item->next;
  }
  goHandleBufferEnd(contextId);

  g_mutex_unlock(&ringBuffer->lock);
}

void ringbuffer_free (RingBuffer * ringBuffer) {
  g_mutex_lock (&ringBuffer->lock);

  RingBufferItem* item = ringBuffer->firstItem;
    while(item != NULL) {
      free (item->content);
      RingBufferItem* nextItem = item->next;
      free (item);
      item = nextItem;
    }

  g_mutex_unlock (&ringBuffer->lock);

  g_mutex_clear (&ringBuffer->lock);

  free (ringBuffer);
}

void setMuteProp(GstPad* audioMixerSinkPad, gboolean mute) {
  g_object_set (audioMixerSinkPad, "mute", mute, NULL);
}
