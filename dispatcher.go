/*
 *  Copyright 2018 Expedia Group.
 *
 *     Licensed under the Apache License, Version 2.0 (the "License");
 *     you may not use this file except in compliance with the License.
 *     You may obtain a copy of the License at
 *
 *         http://www.apache.org/licenses/LICENSE-2.0
 *
 *     Unless required by applicable law or agreed to in writing, software
 *     distributed under the License is distributed on an "AS IS" BASIS,
 *     WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *     See the License for the specific language governing permissions and
 *     limitations under the License.
 *
 */

package haystack

import (
	"fmt"
	"os"
	"time"

	"github.com/opentracing/opentracing-go/log"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

/*Dispatcher dispatches the span object*/
type Dispatcher interface {
	Name() string
	Dispatch(span *_Span)
	Close()
	SetLogger(logger Logger)
}

/*InMemoryDispatcher implements the Dispatcher interface*/
type InMemoryDispatcher struct {
	spans  []*_Span
	logger Logger
}

/*NewInMemoryDispatcher creates a new in memory dispatcher*/
func NewInMemoryDispatcher() Dispatcher {
	return &InMemoryDispatcher{}
}

/*Name gives the Dispatcher name*/
func (d *InMemoryDispatcher) Name() string {
	return "InMemoryDispatcher"
}

/*SetLogger sets the logger to use*/
func (d *InMemoryDispatcher) SetLogger(logger Logger) {
	d.logger = logger
}

/*Dispatch dispatches the span object*/
func (d *InMemoryDispatcher) Dispatch(span *_Span) {
	d.spans = append(d.spans, span)
}

/*Close down the inMemory dispatcher*/
func (d *InMemoryDispatcher) Close() {
	d.spans = nil
}

/*FileDispatcher file dispatcher*/
type FileDispatcher struct {
	fileHandle *os.File
	logger     Logger
}

/*NewFileDispatcher creates a new file dispatcher*/
func NewFileDispatcher(filename string) Dispatcher {
	fd, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	return &FileDispatcher{
		fileHandle: fd,
	}
}

/*Name gives the Dispatcher name*/
func (d *FileDispatcher) Name() string {
	return "FileDispatcher"
}

/*SetLogger sets the logger to use*/
func (d *FileDispatcher) SetLogger(logger Logger) {
	d.logger = logger
}

/*Dispatch dispatches the span object*/
func (d *FileDispatcher) Dispatch(span *_Span) {
	_, err := d.fileHandle.WriteString(span.String())
	if err != nil {
		panic(err)
	}
}

/*Close down the file dispatcher*/
func (d *FileDispatcher) Close() {
	err := d.fileHandle.Close()
	if err != nil {
		panic(err)
	}
}

/*AgentDispatcher agent dispatcher*/
type AgentDispatcher struct {
	conn    *grpc.ClientConn
	client  SpanAgentClient
	timeout time.Duration
	logger  Logger
}

/*NewDefaultAgentDispatcher creates a new haystack-agent dispatcher*/
func NewDefaultAgentDispatcher() Dispatcher {
	return NewAgentDispatcher("haystack-agent", 35000, 3*time.Second)
}

/*NewAgentDispatcher creates a new haystack-agent dispatcher*/
func NewAgentDispatcher(host string, port int, timeout time.Duration) Dispatcher {
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", host, port), grpc.WithInsecure())
	if err != nil {
		panic(fmt.Sprintf("fail to connect to agent with error: %v", err))
	}

	return &AgentDispatcher{
		conn:    conn,
		client:  NewSpanAgentClient(conn),
		timeout: timeout,
	}
}

/*Name gives the Dispatcher name*/
func (d *AgentDispatcher) Name() string {
	return "AgentDispatcher"
}

/*SetLogger sets the logger to use*/
func (d *AgentDispatcher) SetLogger(logger Logger) {
	d.logger = logger
}

/*Dispatch dispatches the span object*/
func (d *AgentDispatcher) Dispatch(span *_Span) {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	result, err := d.client.Dispatch(ctx, &Span{
		TraceId:       span.context.TraceID(),
		SpanId:        span.context.SpanID(),
		ParentSpanId:  span.context.ParentID(),
		ServiceName:   span.ServiceName(),
		OperationName: span.OperationName(),
		StartTime:     span.startTime.UnixNano() / int64(time.Microsecond),
		Duration:      span.duration.Nanoseconds() / int64(time.Microsecond),
		Tags:          d.tags(span),
		Logs:          d.logs(span),
	})

	if err != nil {
		d.logger.Error("Fail to dispatch to haystack-agent with error %v", err)
	} else if result != nil && result.GetErrorMessage() != "" {
		d.logger.Error("Fail to dispatch to haystack-agent with error message :" + result.GetErrorMessage())
	}
}

func (d *AgentDispatcher) logs(span *_Span) []*Log {
	var spanLogs []*Log
	for _, lg := range span.logs {
		spanLogs = append(spanLogs, &Log{
			Timestamp: lg.Timestamp.UnixNano() / int64(time.Microsecond),
			Fields:    d.logFieldsToTags(lg.Fields),
		})
	}
	return spanLogs
}

func (d *AgentDispatcher) logFieldsToTags(fields []log.Field) []*Tag {
	var spanTags []*Tag
	for _, field := range fields {
		spanTags = append(spanTags, d.convertToTag(field.Key(), field.Value()))
	}
	return spanTags
}

func (d *AgentDispatcher) tags(span *_Span) []*Tag {
	var spanTags []*Tag
	for _, tag := range span.tags {
		spanTags = append(spanTags, d.convertToTag(tag.Key, tag.Value))
	}
	return spanTags
}

/*Close down the file dispatcher*/
func (d *AgentDispatcher) Close() {
	err := d.conn.Close()
	if err != nil {
		d.logger.Error("Fail to close the haystack-agent dispatcher %v", err)
	}
}

func (d *AgentDispatcher) convertToTag(key string, value interface{}) *Tag {
	switch v := value.(type) {
	case string:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VStr{
				VStr: value.(string),
			},
			Type: Tag_STRING,
		}
	case int32:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VLong{
				VLong: int64(value.(int32)),
			},
			Type: Tag_LONG,
		}
	case int64:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VLong{
				VLong: value.(int64),
			},
			Type: Tag_LONG,
		}
	case float32:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VDouble{
				VDouble: float64(value.(float32)),
			},
			Type: Tag_DOUBLE,
		}
	case float64:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VDouble{
				VDouble: value.(float64),
			},
			Type: Tag_DOUBLE,
		}
	case bool:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VBool{
				VBool: value.(bool),
			},
			Type: Tag_BOOL,
		}
	case []byte:
		return &Tag{
			Key: key,
			Myvalue: &Tag_VBytes{
				VBytes: value.([]byte),
			},
			Type: Tag_BINARY,
		}
	default:
		panic(fmt.Errorf("unknown format %v", v))
	}
}
