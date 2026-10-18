package thread

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ambrdk/notifykit/telegram"
)

const (
	channel    = "-1001"
	discussion = int64(-1002)
)

type sent struct {
	msg telegram.Message
	id  int64
}

type edit struct {
	chatID string
	id     int64
	text   string
}

type fake struct {
	nextID  int64
	sends   []sent
	edits   []edit
	editErr map[int64]error
	sendErr map[int64]error
	polls   [][]telegram.Update
	offsets []int64
}

func (f *fake) SendMessage(_ context.Context, m telegram.Message) (int64, error) {
	if err := f.sendErr[m.ReplyTo]; err != nil && m.ReplyTo != 0 {
		return 0, err
	}
	f.nextID++
	id := 100 + f.nextID
	f.sends = append(f.sends, sent{msg: m, id: id})
	return id, nil
}

func (f *fake) EditMessageText(_ context.Context, chatID string, id int64, text string) error {
	f.edits = append(f.edits, edit{chatID: chatID, id: id, text: text})
	return f.editErr[id]
}

func (f *fake) Poll(_ context.Context, offset int64, _ time.Duration) ([]telegram.Update, error) {
	f.offsets = append(f.offsets, offset)
	if len(f.polls) == 0 {
		return nil, nil
	}
	next := f.polls[0]
	f.polls = f.polls[1:]
	return next, nil
}

func forward(updateID, copyID, origin int64) telegram.Update {
	return telegram.Update{UpdateID: updateID, Message: &telegram.ChatMessage{MessageID: copyID, ChatID: discussion, IsAutomaticForward: true, ForwardOriginMessageID: origin}}
}

func TestPublishCreatesPostAndComment(t *testing.T) {
	f := &fake{polls: [][]telegram.Update{nil, {forward(7, 501, 101)}}}
	ids, err := New(f, channel, discussion, time.Second).Publish(context.Background(), IDs{}, "post", "comment")
	if err != nil {
		t.Fatal(err)
	}
	if ids != (IDs{PostID: 101, CopyID: 501, CommentID: 102}) {
		t.Fatalf("ids %+v", ids)
	}
	if len(f.sends) != 2 || !f.sends[0].msg.Silent || f.sends[0].msg.ChatID != channel {
		t.Fatalf("sends %+v", f.sends)
	}
	reply := f.sends[1].msg
	if reply.ChatID != fmt.Sprint(discussion) || reply.ReplyTo != 501 || !reply.Silent || reply.Text != "comment" {
		t.Fatalf("reply %+v", reply)
	}
	if last := f.offsets[len(f.offsets)-1]; last != 8 {
		t.Fatalf("offsets %v", f.offsets)
	}
}

func TestPublishEditsExisting(t *testing.T) {
	f := &fake{}
	in := IDs{PostID: 101, CopyID: 501, CommentID: 102}
	ids, err := New(f, channel, discussion, time.Second).Publish(context.Background(), in, "post", "comment")
	if err != nil || ids != in {
		t.Fatalf("ids %+v err %v", ids, err)
	}
	if len(f.sends) != 0 || len(f.edits) != 2 || len(f.offsets) != 0 {
		t.Fatalf("sends %d edits %d polls %d", len(f.sends), len(f.edits), len(f.offsets))
	}
	if f.edits[0] != (edit{chatID: channel, id: 101, text: "post"}) || f.edits[1] != (edit{chatID: fmt.Sprint(discussion), id: 102, text: "comment"}) {
		t.Fatalf("edits %+v", f.edits)
	}
}

func TestPublishSkipsForeignUpdates(t *testing.T) {
	f := &fake{polls: [][]telegram.Update{{forward(10, 400, 999)}, {forward(11, 501, 101)}}}
	ids, err := New(f, channel, discussion, time.Second).Publish(context.Background(), IDs{}, "post", "comment")
	if err != nil || ids.CopyID != 501 {
		t.Fatalf("ids %+v err %v", ids, err)
	}
	if f.offsets[0] != 0 || f.offsets[1] != 11 || f.offsets[len(f.offsets)-1] != 12 {
		t.Fatalf("offsets %v", f.offsets)
	}
}

func TestPublishNoCopyWithinWait(t *testing.T) {
	f := &fake{}
	ids, err := New(f, channel, discussion, 50*time.Millisecond).Publish(context.Background(), IDs{}, "post", "comment")
	if err != nil {
		t.Fatal(err)
	}
	if ids != (IDs{PostID: 101}) || len(f.sends) != 1 {
		t.Fatalf("ids %+v sends %d", ids, len(f.sends))
	}
}

func TestPublishRecreatesGonePost(t *testing.T) {
	f := &fake{nextID: 5, editErr: map[int64]error{101: fmt.Errorf("x: %w", telegram.ErrMessageGone)}, polls: [][]telegram.Update{{forward(3, 601, 106)}}}
	ids, err := New(f, channel, discussion, time.Second).Publish(context.Background(), IDs{PostID: 101, CopyID: 501, CommentID: 102}, "post", "comment")
	if err != nil {
		t.Fatal(err)
	}
	if ids.PostID == 101 || ids.PostID == 0 {
		t.Fatalf("ids %+v", ids)
	}
	for _, e := range f.edits {
		if e.id == 102 {
			t.Fatal("old comment edited after the post was gone")
		}
	}
}

func TestPublishRecreatesGoneComment(t *testing.T) {
	f := &fake{editErr: map[int64]error{102: fmt.Errorf("x: %w", telegram.ErrMessageGone)}}
	ids, err := New(f, channel, discussion, time.Second).Publish(context.Background(), IDs{PostID: 101, CopyID: 501, CommentID: 102}, "post", "comment")
	if err != nil {
		t.Fatal(err)
	}
	if ids.CommentID == 102 || ids.CommentID == 0 || len(f.sends) != 1 || f.sends[0].msg.ReplyTo != 501 {
		t.Fatalf("ids %+v sends %+v", ids, f.sends)
	}
}

func TestPublishCopyGone(t *testing.T) {
	f := &fake{sendErr: map[int64]error{501: fmt.Errorf("x: %w", telegram.ErrMessageGone)}}
	ids, err := New(f, channel, discussion, time.Second).Publish(context.Background(), IDs{PostID: 101, CopyID: 501}, "post", "comment")
	if err != nil {
		t.Fatal(err)
	}
	if ids != (IDs{PostID: 101}) {
		t.Fatalf("ids %+v", ids)
	}
}

func TestPublishWithoutDiscussion(t *testing.T) {
	f := &fake{}
	ids, err := New(f, channel, 0, time.Second).Publish(context.Background(), IDs{}, "post", "comment")
	if err != nil || ids != (IDs{PostID: 101}) || len(f.offsets) != 0 {
		t.Fatalf("ids %+v err %v polls %d", ids, err, len(f.offsets))
	}
}
