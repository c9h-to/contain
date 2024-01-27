// Package appender provides an API to push layers,
// report progress while pushing and return a resulting image+hash
package appender

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	specsv1 "github.com/opencontainers/image-spec/specs-go/v1"
	schema "github.com/turbokube/contain/pkg/schema/v1"
	"go.uber.org/zap"
)

const (
	progressReportMinInterval = "1s"
)

type Appender struct {
	config       schema.ContainConfig
	baseRef      name.Reference
	tagRef       name.Reference
	mediaType    types.MediaType
	layerType    types.MediaType
	craneOptions crane.Options
}

func New(config schema.ContainConfig) (*Appender, error) {
	c := Appender{
		config: config,
	}
	var err error

	c.baseRef, err = name.ParseReference(config.Base)
	if err != nil {
		zap.L().Error("Failed to parse base", zap.String("ref", config.Base), zap.Error(err))
	}
	zap.L().Debug("base image", zap.String("ref", c.baseRef.String()))

	c.tagRef, err = name.ParseReference(config.Tag)
	if err != nil {
		zap.L().Error("Failed to parse result image ref", zap.String("ref", config.Tag), zap.Error(err))
	}
	if c.tagRef != nil {
		zap.L().Debug("target image", zap.String("ref", c.tagRef.String()))
	}

	return &c, nil
}

func (c *Appender) Options() *[]crane.Option {
	zap.L().Fatal("TODO how?")
	return nil
}

// base produces/retrieves the base image
// basically https://github.com/google/go-containerregistry/blob/v0.13.0/cmd/crane/cmd/append.go#L52
func (c *Appender) base() (v1.Image, error) {
	if c.mediaType != "" {
		zap.L().Fatal("contain.Base() has already been invoked")
	}
	var base v1.Image
	var err error
	var mediaType = types.OCIManifestSchema1

	base, err = remote.Image(c.baseRef, c.craneOptions.Remote...)
	if err != nil {
		return nil, fmt.Errorf("pulling %s: %w", c.baseRef.String(), err)
	}
	mediaType, err = base.MediaType()
	if err != nil {
		return nil, fmt.Errorf("getting base image media type: %w", err)
	}

	// https://github.com/google/go-containerregistry/blob/v0.13.0/pkg/crane/append.go#L60
	if mediaType == types.OCIManifestSchema1 {
		c.layerType = types.OCILayer
	} else {
		c.layerType = types.DockerLayer
	}
	c.mediaType = mediaType

	return base, nil
}

// Append is what you call once layers are ready
func (c *Appender) Append(layers ...v1.Layer) (v1.Hash, error) {
	// Platform support remains to be verified with for example docker hub
	// See also https://github.com/google/go-containerregistry/issues/1456 and https://github.com/google/go-containerregistry/pull/1561
	if len(c.config.Platforms) > 1 {
		zap.L().Warn("unsupported multiple platforms, falling back to all", zap.Strings("platforms", c.config.Platforms))
	}
	if len(c.config.Platforms) == 1 {
		zap.L().Warn("unsupported single platform, falling back to all", zap.String("platform", c.config.Platforms[0]))
	}
	noresult := v1.Hash{}
	base, err := c.base()
	if err != nil {
		zap.L().Error("Failed to get base image", zap.Error(err))
		return noresult, err
	}
	baseDigest, err := base.Digest()
	if err != nil {
		zap.L().Error("Failed to get base image digest", zap.Error(err))
	}
	img, err := mutate.AppendLayers(base, layers...)
	if err != nil {
		zap.L().Error("Failed to append layers", zap.Error(err))
		return noresult, err
	}
	img = c.annotate(img, baseDigest)
	if err != nil {
		zap.L().Error("Failed to annotate", zap.Error(err))
		return noresult, err
	}
	imgDigest, err := img.Digest()
	if err != nil {
		zap.L().Error("Failed to get result image digest", zap.Error(err))
		return noresult, err
	}
	err = c.push(img)
	if err != nil {
		zap.L().Error("Failed to push", zap.Error(err))
		return noresult, err
	}
	zap.L().Info("pushed",
		zap.String("digest", imgDigest.String()),
	)
	return imgDigest, nil
}

// annotate is called after append
func (c *Appender) annotate(image v1.Image, baseDigest v1.Hash) v1.Image {
	// https://github.com/google/go-containerregistry/blob/v0.13.0/cmd/crane/cmd/append.go#L71
	a := map[string]string{
		specsv1.AnnotationBaseImageDigest: baseDigest.String(),
	}
	if _, ok := c.baseRef.(name.Tag); ok {
		a[specsv1.AnnotationBaseImageName] = fmt.Sprintf("/%s:%s",
			c.baseRef.Context().RepositoryStr(),
			c.baseRef.Identifier(),
		)
	}
	img := mutate.Annotations(image, a).(v1.Image)
	return img
}

func (c *Appender) push(image v1.Image) error {
	mediaType, err := image.MediaType()
	if err != nil {
		return err
	}
	zap.L().Info("pushing", zap.String("mediaType", string(mediaType)))

	debounce, err := time.ParseDuration(progressReportMinInterval)
	if err != nil {
		zap.L().Fatal("failed to parse debounce interval", zap.String("value", progressReportMinInterval), zap.Error(err))
	}

	progressChan := make(chan v1.Update, 200)
	errChan := make(chan error, 2)

	go func() {
		options := append(c.craneOptions.Remote, remote.WithProgress(progressChan))
		errChan <- remote.Write(
			c.tagRef,
			image,
			options...,
		)
	}()

	logger := zap.L()
	nextProgress := time.Now().Add(debounce)

	for update := range progressChan {
		if update.Error != nil {
			logger.Error("push update", zap.Error(update.Error))
			errChan <- update.Error
			break
		}

		if update.Complete == update.Total {
			logger.Info("pushed", zap.Int64("completed", update.Complete), zap.Int64("total", update.Total))
		} else {
			if time.Now().After(nextProgress) {
				nextProgress = time.Now().Add(debounce)
				logger.Info("push", zap.Int64("completed", update.Complete), zap.Int64("total", update.Total))
			}
		}
	}

	return <-errChan
}

func (c *Appender) LayerType() types.MediaType {
	if c.layerType == "" {
		zap.L().Fatal("Can not return media type before Base has been called")
	}
	return c.layerType
}
