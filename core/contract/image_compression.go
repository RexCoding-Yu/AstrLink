package contract

import "fmt"

// ImageCompressionMode chooses how request images are re-encoded as WebP.
type ImageCompressionMode string

const (
	// ImageCompressionLossless keeps every pixel. It re-encodes PNG and
	// single-frame GIF images; a JPEG would only grow.
	ImageCompressionLossless ImageCompressionMode = "lossless"
	// ImageCompressionLossy encodes at ImageCompression.Quality and also
	// re-encodes JPEG images. A PNG or GIF whose lossless WebP is smaller
	// is sent lossless.
	ImageCompressionLossy ImageCompressionMode = "lossy"
)

const DefaultImageCompressionQuality = 85

// ImageCompression re-encodes the base64 images of inference requests as
// WebP before they are forwarded, so fewer bytes go upstream. An image is
// replaced only when its WebP is smaller; image URLs, animated GIFs and WebP
// images are forwarded as they are.
type ImageCompression struct {
	Enabled bool                 `json:"enabled"`
	Mode    ImageCompressionMode `json:"mode"`
	// Quality is the lossy encoder's quality, 1 through 100. It is kept
	// while the lossless mode is chosen.
	Quality int `json:"quality"`
}

func DefaultImageCompression() ImageCompression {
	return ImageCompression{Mode: ImageCompressionLossless, Quality: DefaultImageCompressionQuality}
}

func (settings ImageCompression) Validate() error {
	switch settings.Mode {
	case ImageCompressionLossless, ImageCompressionLossy:
	default:
		return fmt.Errorf("image_compression.mode must be lossless or lossy")
	}
	if settings.Quality < 1 || settings.Quality > 100 {
		return fmt.Errorf("image_compression.quality must be between 1 and 100")
	}
	return nil
}

func (settings *ImageCompression) UnmarshalJSON(data []byte) error {
	type document ImageCompression
	var value document
	if err := decodePolicy(data, &value, []string{"enabled", "mode", "quality"}, nil); err != nil {
		return err
	}
	if err := ImageCompression(value).Validate(); err != nil {
		return err
	}
	*settings = ImageCompression(value)
	return nil
}
