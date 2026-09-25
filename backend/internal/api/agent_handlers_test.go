package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestValidateAgentAttachmentEnforcesImageContract(t *testing.T) {
	filename, mediaType, err := validateAgentAttachment(agentAttachmentInput{
		Filename: "  课表截图.png  ", MediaType: "image/png", SizeBytes: 1024,
	})
	if err != nil || filename != "课表截图.png" || mediaType != "image/png" {
		t.Fatalf("valid image = (%q, %q, %v)", filename, mediaType, err)
	}
	for _, input := range []agentAttachmentInput{
		{Filename: "截图.svg", MediaType: "image/svg+xml", SizeBytes: 1024},
		{Filename: "截图.png", MediaType: "image/jpeg", SizeBytes: 1024},
		{Filename: "截图.png", MediaType: "image/png", SizeBytes: agentImageMaxBytes + 1},
	} {
		if _, _, err := validateAgentAttachment(input); err == nil {
			t.Fatalf("accepted invalid attachment %#v", input)
		}
	}
}

func TestValidateAgentImageBytesChecksActualFormat(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 8))
	canvas.Set(1, 1, color.Black)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentImageBytes(encoded.Bytes(), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentImageBytes(encoded.Bytes(), "image/jpeg"); err == nil {
		t.Fatal("PNG bytes were accepted as JPEG")
	}
	if err := validateAgentImageBytes([]byte("not an image"), "image/png"); err == nil {
		t.Fatal("arbitrary bytes were accepted as an image")
	}
	e2eFixture, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil || validateAgentImageBytes(e2eFixture, "image/png") != nil {
		t.Fatal("Playwright screenshot fixture is not a valid PNG")
	}
}

func TestParseAgentAttachmentIDsRejectsDuplicatesAndTooMany(t *testing.T) {
	ids, err := parseAgentAttachmentIDs([]string{"7", "9"})
	if err != nil || len(ids) != 2 || ids[0] != 7 || ids[1] != 9 {
		t.Fatalf("ids = %#v, error = %v", ids, err)
	}
	if _, err := parseAgentAttachmentIDs([]string{"7", "7"}); err == nil {
		t.Fatal("duplicate attachment ID was accepted")
	}
	if _, err := parseAgentAttachmentIDs(strings.Split("1,2,3,4,5", ",")); err == nil {
		t.Fatal("more than four attachments were accepted")
	}
}
