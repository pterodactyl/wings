package server

import "testing"

func TestProtectedStateCancelsSftpSessions(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Server)
	}{
		{
			name: "installing",
			set: func(s *Server) {
				s.SetInstalling(true)
			},
		},
		{
			name: "transferring",
			set: func(s *Server) {
				s.SetTransferring(true)
			},
		},
		{
			name: "restoring",
			set: func(s *Server) {
				s.SetRestoring(true)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := New(nil)
			if err != nil {
				t.Fatal(err)
			}

			ctx := srv.Sftp().Context("user")
			tt.set(srv)

			select {
			case <-ctx.Done():
			default:
				t.Fatal("expected SFTP session context to be canceled")
			}
		})
	}
}
