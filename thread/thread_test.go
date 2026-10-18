package thread

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ambrdk/notifykit/telegram"
	"github.com/ambrdk/notifykit/telegram/telegramtest"
)

const (
	channel    = "-1001"
	discussion = int64(-1002)
	group      = "-1002"
)

type queueCopies struct {
	f       *telegramtest.Fake
	pending bool
	calls   int
}

func (q *queueCopies) Find(ctx context.Context, discussionID, postID int64) (int64, bool, error) {
	q.calls++
	ups, err := q.f.Poll(ctx, 0, 0)
	if err != nil {
		return 0, false, err
	}
	for _, u := range ups {
		if m := u.Message; m != nil && m.IsAutomaticForward && m.ChatID == discussionID && m.ForwardOriginMessageID == postID {
			return m.MessageID, false, nil
		}
	}
	return 0, q.pending, nil
}

func setup(discussionID int64, opts ...Option) (*Thread, *telegramtest.Fake, *queueCopies) {
	f := telegramtest.New(channel, discussionID)
	q := &queueCopies{f: f}
	return New(f, channel, q, opts...), f, q
}

func comment(text string) func(int) string {
	return func(int) string { return text }
}

func post(text, c string) Post {
	return Post{Text: text, Comment: comment(c)}
}

func TestPublishCreatesPostAndComment(t *testing.T) {
	th, f, _ := setup(discussion)
	out, err := th.Publish(context.Background(), IDs{}, post("p", "c"))
	if err != nil || out.Status != Commented {
		t.Fatalf("out %+v err %v", out, err)
	}
	posts := f.Messages(channel)
	if len(posts) != 1 || !posts[0].Silent || posts[0].ID != out.IDs.PostID {
		t.Fatalf("posts %+v", posts)
	}
	msgs := f.Messages(group)
	if len(msgs) != 2 || msgs[1].ReplyTo != out.IDs.CopyID || msgs[1].Text != "c" || msgs[1].ID != out.IDs.CommentID || !msgs[1].Silent {
		t.Fatalf("discussion %+v ids %+v", msgs, out.IDs)
	}
}

func TestPublishLoud(t *testing.T) {
	th, f, _ := setup(discussion)
	p := post("p", "c")
	p.Loud = true
	if _, err := th.Publish(context.Background(), IDs{}, p); err != nil {
		t.Fatal(err)
	}
	if f.Messages(channel)[0].Silent {
		t.Fatal("loud post sent silently")
	}
}

func TestPublishEditsExisting(t *testing.T) {
	th, f, q := setup(discussion)
	ctx := context.Background()
	first, _ := th.Publish(ctx, IDs{}, post("p", "c"))
	second, err := th.Publish(ctx, first.IDs, post("p2", "c2"))
	if err != nil || second.Status != Commented || second.IDs != first.IDs {
		t.Fatalf("second %+v err %v", second, err)
	}
	if f.Calls("sendMessage") != 2 || q.calls != 1 {
		t.Fatalf("sends %d finds %d", f.Calls("sendMessage"), q.calls)
	}
	if f.Messages(channel)[0].Text != "p2" || f.Messages(group)[1].Text != "c2" {
		t.Fatal("texts not edited")
	}
}

func TestPublishPostOnly(t *testing.T) {
	th, f, q := setup(discussion)
	out, err := th.Publish(context.Background(), IDs{}, Post{Text: "p"})
	if err != nil || out.Status != Posted || out.IDs.PostID == 0 || out.IDs.CommentID != 0 {
		t.Fatalf("out %+v err %v", out, err)
	}
	if q.calls != 0 || len(f.Messages(group)) != 1 {
		t.Fatalf("finds %d discussion %+v", q.calls, f.Messages(group))
	}
}

func TestPublishRecreatesGonePost(t *testing.T) {
	th, f, _ := setup(discussion)
	ctx := context.Background()
	first, _ := th.Publish(ctx, IDs{}, post("p", "c"))
	f.Delete(channel, first.IDs.PostID)
	out, err := th.Publish(ctx, first.IDs, post("p2", "c2"))
	if err != nil || out.Status != Commented || out.IDs.PostID == first.IDs.PostID || out.IDs.CommentID == first.IDs.CommentID {
		t.Fatalf("out %+v err %v", out, err)
	}
	if got := f.Messages(channel); len(got) != 1 || got[0].Text != "p2" {
		t.Fatalf("posts %+v", got)
	}
}

func TestPublishFinalGonePost(t *testing.T) {
	th, f, _ := setup(discussion)
	ctx := context.Background()
	first, _ := th.Publish(ctx, IDs{}, post("p", "c"))
	f.Delete(channel, first.IDs.PostID)
	p := post("p2", "c2")
	p.Final = true
	out, err := th.Publish(ctx, first.IDs, p)
	if err != nil || out.Status != PostGone || out.IDs != (IDs{}) {
		t.Fatalf("out %+v err %v", out, err)
	}
	if f.Calls("sendMessage") != 2 {
		t.Fatalf("sends %d", f.Calls("sendMessage"))
	}
}

func TestPublishRecreatesGoneComment(t *testing.T) {
	th, f, _ := setup(discussion)
	ctx := context.Background()
	first, _ := th.Publish(ctx, IDs{}, post("p", "c"))
	f.Delete(group, first.IDs.CommentID)
	out, err := th.Publish(ctx, first.IDs, post("p", "c2"))
	if err != nil || out.Status != Commented || out.IDs.CommentID == first.IDs.CommentID || out.IDs.CopyID != first.IDs.CopyID {
		t.Fatalf("out %+v err %v", out, err)
	}
	last := f.Messages(group)[len(f.Messages(group))-1]
	if last.ReplyTo != first.IDs.CopyID || last.Text != "c2" {
		t.Fatalf("comment %+v", last)
	}
}

func TestPublishCopyGoneGoesUnthreaded(t *testing.T) {
	th, f, _ := setup(discussion)
	ctx := context.Background()
	first, _ := th.Publish(ctx, IDs{}, Post{Text: "p"})
	ids := first.IDs
	ids.CopyID = 77
	out, err := th.Publish(ctx, ids, post("p", "c"))
	if err != nil || out.Status != Unthreaded || out.IDs.CopyID != 0 || out.IDs.CommentID == 0 {
		t.Fatalf("out %+v err %v", out, err)
	}
	last := f.Messages(group)[len(f.Messages(group))-1]
	if last.ReplyTo != 0 || last.Text != "c" {
		t.Fatalf("comment %+v", last)
	}
}

func TestPublishUnthreadedCommentGone(t *testing.T) {
	th, f, q := setup(discussion)
	ctx := context.Background()
	f.HoldCopies(true)
	q.pending = true
	first, _ := th.Publish(ctx, IDs{}, post("p", "c"))
	second, _ := th.Publish(ctx, first.IDs, post("p", "c"))
	if second.Status != Unthreaded {
		t.Fatalf("second %+v", second)
	}
	f.Delete(group, second.IDs.CommentID)
	finds := q.calls
	out, err := th.Publish(ctx, second.IDs, post("p", "c3"))
	if err != nil || out.Status != Unthreaded || out.IDs.CommentID == second.IDs.CommentID {
		t.Fatalf("out %+v err %v", out, err)
	}
	last := f.Messages(group)[len(f.Messages(group))-1]
	if last.ReplyTo != 0 || last.Text != "c3" || q.calls != finds {
		t.Fatalf("comment %+v finds %d->%d", last, finds, q.calls)
	}
}

func TestPublishCopyPendingThenUnthreaded(t *testing.T) {
	th, f, q := setup(discussion)
	ctx := context.Background()
	f.HoldCopies(true)
	q.pending = true
	first, err := th.Publish(ctx, IDs{}, post("p", "c"))
	if err != nil || first.Status != CopyPending || !first.IDs.CopyWaited || first.IDs.CommentID != 0 {
		t.Fatalf("first %+v err %v", first, err)
	}
	second, err := th.Publish(ctx, first.IDs, post("p", "c"))
	if err != nil || second.Status != Unthreaded || second.IDs.CommentID == 0 {
		t.Fatalf("second %+v err %v", second, err)
	}
	again, err := th.Publish(ctx, second.IDs, post("p", "c2"))
	if err != nil || again.Status != Unthreaded || again.IDs.CommentID != second.IDs.CommentID {
		t.Fatalf("again %+v err %v", again, err)
	}
}

func TestPublishNoCopy(t *testing.T) {
	th, f, _ := setup(discussion)
	f.HoldCopies(true)
	out, err := th.Publish(context.Background(), IDs{}, post("p", "c"))
	if err != nil || out.Status != NoCopy || !out.IDs.CopyWaited {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestPublishNoDiscussion(t *testing.T) {
	th, f, q := setup(0)
	out, err := th.Publish(context.Background(), IDs{}, post("p", "c"))
	if err != nil || out.Status != NoDiscussion || out.IDs.PostID == 0 || q.calls != 0 {
		t.Fatalf("out %+v err %v finds %d", out, err, q.calls)
	}
	if len(f.Messages(channel)) != 1 {
		t.Fatal("post missing")
	}
}

func TestPublishDiscussionRetried(t *testing.T) {
	th, f, _ := setup(discussion)
	ctx := context.Background()
	f.FailGetChat(errors.New("down"))
	first, err := th.Publish(ctx, IDs{}, post("p", "c"))
	if err != nil || first.Status != NoDiscussion {
		t.Fatalf("first %+v err %v", first, err)
	}
	second, err := th.Publish(ctx, first.IDs, post("p", "c"))
	if err != nil || second.Status != Commented {
		t.Fatalf("second %+v err %v", second, err)
	}
	if _, err := th.Publish(ctx, second.IDs, post("p", "c")); err != nil || f.Calls("getChat") != 2 {
		t.Fatalf("getChat calls %d err %v", f.Calls("getChat"), err)
	}
}

func TestPublishRateLimitRetried(t *testing.T) {
	th, f, _ := setup(discussion, WithMaxWait(50*time.Millisecond))
	f.RateLimit(1, 10*time.Millisecond)
	out, err := th.Publish(context.Background(), IDs{}, post("p", "c"))
	if err != nil || out.Status != Commented {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestPublishRateLimitAfterPost(t *testing.T) {
	th, f, _ := setup(discussion, WithMaxWait(10*time.Millisecond))
	ctx := context.Background()
	first, _ := th.Publish(ctx, IDs{}, Post{Text: "p"})
	f.RateLimit(5, time.Second)
	out, err := th.Publish(ctx, first.IDs, post("p2", "c"))
	var limited *telegram.RateLimitedError
	if !errors.As(err, &limited) || out.IDs.PostID != first.IDs.PostID {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestPublishRateLimitHonoursContext(t *testing.T) {
	th, f, _ := setup(discussion, WithMaxWait(time.Minute))
	f.RateLimit(1, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := th.Publish(ctx, IDs{}, post("p", "c")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestPublishCommentSkip(t *testing.T) {
	th, f, _ := setup(discussion)
	var skips []int
	render := func(skip int) string {
		skips = append(skips, skip)
		return strings.Repeat("x", 5000-skip*400)
	}
	out, err := th.Publish(context.Background(), IDs{}, Post{Text: "p", Comment: render})
	if err != nil || out.Status != Commented {
		t.Fatalf("out %+v err %v", out, err)
	}
	got := f.Messages(group)[1].Text
	if utf8.RuneCountInString(got) > 4096 || got != strings.Repeat("x", 5000-3*400) || skips[len(skips)-1] != 3 {
		t.Fatalf("len %d skips %v", utf8.RuneCountInString(got), skips)
	}
}

func TestPublishCommentCannotShrink(t *testing.T) {
	th, f, _ := setup(discussion)
	calls := 0
	render := func(int) string {
		calls++
		return "<pre>" + strings.Repeat("y", 5000) + "</pre>"
	}
	if _, err := th.Publish(context.Background(), IDs{}, Post{Text: "p", Comment: render}); err != nil {
		t.Fatal(err)
	}
	if got := f.Messages(group)[1].Text; utf8.RuneCountInString(got) > 4096 || calls > 3 {
		t.Fatalf("len %d calls %d", utf8.RuneCountInString(got), calls)
	}
}

func TestPublishLongPost(t *testing.T) {
	th, f, _ := setup(discussion)
	if _, err := th.Publish(context.Background(), IDs{}, Post{Text: strings.Repeat("z", 5000)}); err != nil {
		t.Fatal(err)
	}
	if got := f.Messages(channel)[0].Text; utf8.RuneCountInString(got) > 4096 {
		t.Fatalf("len %d", utf8.RuneCountInString(got))
	}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func TestFitDenseEscapes(t *testing.T) {
	for name, in := range map[string]string{
		"amp":  strings.Repeat("&amp;", 5000),
		"json": strings.Repeat(`{&#34;a&#34;:&#34;b&#34;,&#34;c&#34;:&lt;d&gt;}`, 400),
	} {
		got := fit(in)
		if utf16Len(got) > 4096 || !strings.HasSuffix(got, "…") || len(got) < 1000 {
			t.Fatalf("%s: len %d", name, utf16Len(got))
		}
	}
}

func TestFitCountsUTF16(t *testing.T) {
	got := fit(strings.Repeat("🟡", 3000))
	if utf16Len(got) > 4096 {
		t.Fatalf("utf16 length %d", utf16Len(got))
	}
}

func TestPublishCommentUTF16(t *testing.T) {
	th, f, _ := setup(discussion)
	var skips []int
	render := func(skip int) string {
		skips = append(skips, skip)
		return strings.Repeat("🟡", 3000-skip*500)
	}
	if _, err := th.Publish(context.Background(), IDs{}, Post{Text: "p", Comment: render}); err != nil {
		t.Fatal(err)
	}
	if got := f.Messages(group)[1].Text; utf16Len(got) > 4096 || skips[len(skips)-1] != 2 {
		t.Fatalf("utf16 %d skips %v", utf16Len(got), skips)
	}
}

func TestPublishFinalWithoutCopyGoesUnthreaded(t *testing.T) {
	th, f, q := setup(discussion)
	ctx := context.Background()
	f.HoldCopies(true)
	q.pending = true
	first, _ := th.Publish(ctx, IDs{}, Post{Text: "p"})
	ids := first.IDs
	p := post("p2", "c")
	p.Final = true
	out, err := th.Publish(ctx, ids, p)
	if err != nil || out.Status != Unthreaded || out.IDs.CommentID == 0 {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestFitStripsHTML(t *testing.T) {
	in := "<b>" + strings.Repeat("я &amp; ", 1000) + "</b>"
	got := fit(in)
	if utf8.RuneCountInString(got) > 4096 || !strings.HasSuffix(got, "…") || strings.Contains(got, "<b>") || !strings.Contains(got, "я &amp; я") {
		t.Fatalf("fit %q", got[:80])
	}
	if fit("<b>short</b>") != "<b>short</b>" {
		t.Fatal("short text changed")
	}
}
