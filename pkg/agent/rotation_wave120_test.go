package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/pika"
)

// Волна 120 (срез 3): сигнал датчика приводит к реальной ротации
// (дефолтный агент тестового цикла уже на PikaSessionStore).
func TestRotateSessionWithNotice_Rotates(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})

	db, err := pika.Migrate(":memory:")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer db.Close()
	bm, err := pika.NewBotMemory(db)
	if err != nil {
		t.Fatalf("botmemory: %v", err)
	}
	defer bm.Close()

	ps := pika.NewPikaSessionStore(bm)
	agent.Sessions = ps
	sl := ps.Session("test-session")
	sl.EnsureSession("test-session")
	oldID := sl.SessionID()
	if oldID == "" {
		t.Fatal("EnsureSession did not assign session ID")
	}

	// Ротируем явно подготовленный lifecycle (ts.sessionKey в тестовом
	// хелпере пустой; прод-путь берёт ключ из настоящего turnState).
	// ID = baseKey:unix-секунды — внутри одной секунды совпадает, поэтому
	// проверяем сам факт ротации через OnRotate-колбэк, а не смену ID.
	rotated := false
	sl.OnRotate(func(old string) {
		rotated = true
		if old != oldID {
			panic("callback got unexpected old session ID")
		}
	})
	al.rotateSessionWithNotice(context.Background(), ts, sl, "тест")

	if !rotated {
		t.Error("OnRotate callback not fired — rotation did not happen")
	}
}
