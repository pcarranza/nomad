package driver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_DockerImageProgressManager(t *testing.T) {

	pm := &imageProgressManager{
		imageProgress: &imageProgress{
			timestamp: time.Now(),
			layers:    make(map[string]*layerProgress),
		},
	}

	_, err := pm.Write([]byte(`{"status":"Pulling from library/golang","id":"1.9.5"}
{"status":"Pulling fs layer","progressDetail":{},"id":"c73ab1c6897b"}
{"status":"Pulling fs layer","progressDetail":{},"id":"1ab373b3deae"}
`))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(pm.imageProgress.layers), "number of layers should be 2")

	cur := pm.currentBytes()
	assert.Zero(t, cur)
	tot := pm.totalBytes()
	assert.Zero(t, tot)

	_, err = pm.Write([]byte(`{"status":"Pulling fs layer","progress`))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(pm.imageProgress.layers), "number of layers should be 2")

	_, err = pm.Write([]byte(`Detail":{},"id":"b542772b4177"}` + "\n"))
	assert.NoError(t, err)
	assert.Equal(t, 3, len(pm.imageProgress.layers), "number of layers should be 3")

	_, err = pm.Write([]byte(`{"status":"Downloading","progressDetail":{"current":45800,"total":4335495},"progress":"[\u003e                                                  ]   45.8kB/4.335MB","id":"b542772b4177"}
{"status":"Downloading","progressDetail":{"current":113576,"total":11108010},"progress":"[\u003e                                                  ]  113.6kB/11.11MB","id":"1ab373b3deae"}
{"status":"Downloading","progressDetail":{"current":694257,"total":4335495},"progress":"[========\u003e                                          ]  694.3kB/4.335MB","id":"b542772b4177"}` + "\n"))
	assert.NoError(t, err)
	assert.Equal(t, 3, len(pm.imageProgress.layers), "number of layers should be 3")
	assert.Equal(t, int64(807833), pm.currentBytes())
	assert.Equal(t, int64(15443505), pm.totalBytes())

	_, err = pm.Write([]byte(`{"status":"Download complete","progressDetail":{},"id":"b542772b4177"}` + "\n"))
	assert.NoError(t, err)
	assert.Equal(t, 3, len(pm.imageProgress.layers), "number of layers should be 3")
	assert.Equal(t, int64(4449071), pm.currentBytes())
	assert.Equal(t, int64(15443505), pm.totalBytes())
}
