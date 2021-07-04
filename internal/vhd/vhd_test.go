package vhd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/pterodactyl/wings/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	config.Set(&config.Configuration{
		AuthenticationToken: "token123",
		System: config.SystemConfiguration{
			User: struct {
				Uid int
				Gid int
			}{Uid: 10, Gid: 10},
		},
	})
}

type mockCmd struct {
	run    func() error
	output func() ([]byte, error)
	string func() string
}

func (m *mockCmd) Run() error {
	if m.run != nil {
		return m.run()
	}
	return nil
}

func (m *mockCmd) Output() ([]byte, error) {
	if m.output != nil {
		return m.output()
	}
	return nil, nil
}

func (m *mockCmd) String() string {
	if m.string != nil {
		return m.string()
	}
	return ""
}

var _ Commander = (*mockCmd)(nil)

type mockedExitCode struct {
	code int
}

func (m *mockedExitCode) ExitCode() int {
	return m.code
}

func (m *mockedExitCode) Error() string {
	return fmt.Sprintf("mocked exit code: code %d", m.code)
}

func newMockDisk(c CommanderProvider) *Disk {
	commander := func(ctx context.Context, name string, args ...string) Commander {
		return &mockCmd{}
	}
	w := commander
	if c != nil {
		w = c
	}
	return New(100 * 1024 * 1024, "/disk.img", "/mnt", WithFs(afero.NewMemMapFs()), WithCommander(w))
}

func Test_New(t *testing.T) {
	t.Run("creates expected struct", func(t *testing.T) {
		d := New(100 * 1024 * 1024, "/disk.img", "/mnt")
		assert.NotNil(t, d)
		assert.Equal(t, int64(100 * 1024 * 1024), d.size)
		assert.Equal(t, "/disk.img", d.diskPath)
		assert.Equal(t, "/mnt", d.mountAt)

		// Ensure by default we get a commander interface returned and that it
		// returns an *exec.Cmd.
		o := d.commander(context.TODO(), "foo", "-bar")
		assert.NotNil(t, o)
		_, ok := o.(Commander)
		assert.True(t, ok)
		_, ok = o.(*exec.Cmd)
		assert.True(t, ok)
	})

	t.Run("creates an instance with custom options", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		cprov := struct {
			Commander
		}{}
		c := func(ctx context.Context, name string, args ...string) Commander {
			return &cprov
		}

		d := New(100, "/disk.img", "/mnt", WithFs(fs), WithCommander(c))
		assert.NotNil(t, d)
		assert.Same(t, fs, d.fs)
		assert.Same(t, &cprov, d.commander(context.TODO(), ""))
	})

	t.Run("panics if either path is empty", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = New(100, "", "/bar")
		})

		assert.Panics(t, func() {
			_ = New(100, "/foo", "")
		})
	})
}

func TestDisk_Exists(t *testing.T) {
	t.Run("it exists", func(t *testing.T) {
		d := newMockDisk(nil)
		f, err := d.fs.Create("/disk.img")
		require.NoError(t, err)
		_ = f.Close()

		exists, err := d.Exists()
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("it does not exist", func(t *testing.T) {
		d := newMockDisk(nil)
		exists, err := d.Exists()
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("it reports errors", func(t *testing.T) {
		d := newMockDisk(nil)
		err := d.fs.Mkdir("/disk.img", 0600)
		require.NoError(t, err)

		exists, err := d.Exists()
		assert.Error(t, err)
		assert.False(t, exists)
		assert.EqualError(t, err, ErrInvalidDiskPathTarget.Error())
	})
}

func TestDisk_IsMounted(t *testing.T) {
	t.Run("executes command and finds mounted disk", func(t *testing.T) {
		is := assert.New(t)
		var called bool

		pctx := context.TODO()
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			called = true
			is.Same(pctx, ctx)
			is.Equal("grep", name)
			is.Len(args, 3)
			is.Equal([]string{"-qs", "/mnt ext4", "/proc/mounts"}, args)

			return &mockCmd{}
		}

		disk := newMockDisk(cmd)
		mnt, err := disk.IsMounted(pctx)
		is.NoError(err)
		is.True(mnt)
		is.True(called)
	})

	t.Run("handles exit code 1 gracefully", func(t *testing.T) {
		var called bool
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			called = true
			return &mockCmd{
				run: func() error {
					return &mockedExitCode{code: 1}
				},
			}
		}

		disk := newMockDisk(cmd)
		mnt, err := disk.IsMounted(context.TODO())
		assert.NoError(t, err)
		assert.False(t, mnt)
		assert.True(t, called)
	})

	t.Run("handles unexpected errors successfully", func(t *testing.T) {
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					return &mockedExitCode{code: 3}
				},
			}
		}

		disk := newMockDisk(cmd)
		mnt, err := disk.IsMounted(context.TODO())
		assert.Error(t, err)
		assert.False(t, mnt)
	})
}

func TestDisk_Mount(t *testing.T) {
	failedCmd := func(ctx context.Context, name string, args ...string) Commander {
		return &mockCmd{run: func() error {
			return &mockedExitCode{code: 1}
		}}
	}

	t.Run("error is returned if mount point is not a directory", func(t *testing.T) {
		disk := newMockDisk(failedCmd)
		_, err := disk.fs.Create("/mnt")
		require.NoError(t, err)

		err = disk.Mount(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrMountPathNotDirectory.Error())
	})

	t.Run("error is returned if mount point cannot be created", func(t *testing.T) {
		disk := newMockDisk(failedCmd)
		disk.fs = afero.NewReadOnlyFs(disk.fs)

		err := disk.Mount(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, "vhd: failed to create mount path: operation not permitted")
	})

	t.Run("error is returned if already mounted", func(t *testing.T) {
		disk := newMockDisk(nil)
		err := disk.Mount(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrFilesystemMounted.Error())
	})

	t.Run("error is returned if mount command fails", func(t *testing.T) {
		var called bool
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					return &mockedExitCode{code: 1}
				},
				output: func() ([]byte, error) {
					called = true

					assert.Equal(t, "mount", name)
					assert.Equal(t, []string{"-t", "auto", "-o", "loop", "/disk.img", "/mnt"}, args)

					return nil, &exec.ExitError{
						ProcessState: &os.ProcessState{},
						Stderr: []byte("foo bar.\n"),
					}
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.Mount(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, "vhd: failed to mount disk: foo bar: exit status 0")
		assert.True(t, called)
	})

	t.Run("disk can be mounted at existing path", func(t *testing.T) {
		disk := newMockDisk(failedCmd)
		require.NoError(t, disk.fs.Mkdir("/mnt", 0600))

		err := disk.Mount(context.TODO())
		assert.NoError(t, err)
	})

	t.Run("disk can be mounted at non-existing path", func(t *testing.T) {
		disk := newMockDisk(failedCmd)
		err := disk.Mount(context.TODO())
		assert.NoError(t, err)

		st, err := disk.fs.Stat("/mnt")
		assert.NoError(t, err)
		assert.True(t, st.IsDir())
	})
}

func TestDisk_Unmount(t *testing.T) {
	t.Run("can unmount a disk", func(t *testing.T) {
		is := assert.New(t)
		pctx := context.TODO()

		var called bool
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			called = true

			is.Same(pctx, ctx)
			is.Equal("umount", name)
			is.Equal([]string{"/mnt"}, args)

			return &mockCmd{}
		}

		disk := newMockDisk(cmd)
		err := disk.Unmount(pctx)
		is.NoError(err)
		is.True(called)
	})

	t.Run("handles exit code 32 correctly", func(t *testing.T) {
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					return &mockedExitCode{code: 32}
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.Unmount(context.TODO())
		assert.NoError(t, err)
	})

	t.Run("non code 32 errors are returned as error", func(t *testing.T) {
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					return &mockedExitCode{code: 1}
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.Unmount(context.TODO())
		assert.Error(t, err)
	})

	t.Run("errors without ExitCode function are returned", func(t *testing.T) {
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					return errors.New("foo bar")
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.Unmount(context.TODO())
		assert.Error(t, err)
	})
}

func TestDisk_Allocate(t *testing.T) {
	t.Run("disk is unmounted before allocating space", func(t *testing.T) {
		var called bool
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				output: func() ([]byte, error) {
					called = true
					assert.Equal(t, "fallocate", name)
					assert.Equal(t, []string{"-l", "102400K", "/disk.img"}, args)
					return nil, nil
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.fs.Mkdir("/mnt", 0600)
		require.NoError(t, err)

		err = disk.Allocate(context.TODO())
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("disk space is allocated even when not exists", func(t *testing.T) {
		disk := newMockDisk(nil)
		err := disk.Allocate(context.TODO())
		assert.NoError(t, err)
	})

	t.Run("error is returned if command fails", func(t *testing.T) {
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				output: func() ([]byte, error) {
					return nil, &exec.ExitError{
						ProcessState: &os.ProcessState{},
						Stderr: []byte("foo bar.\n"),
					}
				},
			}
		}

		disk := newMockDisk(cmd)
		_, err := disk.fs.Create("/disk.img")
		require.NoError(t, err)

		err = disk.Allocate(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, "vhd: failed to execute fallocate command: foo bar: exit status 0")
	})
}

func TestDisk_MakeFilesystem(t *testing.T) {
	t.Run("filesystem is created if not found in /etc/fstab", func(t *testing.T) {
		var called bool
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					// Expect the call from IsMounted here and just return what we need
					// to indicate that nothing is currently mounted.
					if name == "grep" {
						return &mockedExitCode{code: 1}
					}
					called = true
					assert.Equal(t, "mkfs", name)
					assert.Equal(t, []string{"-t", "ext4", "/disk.img"}, args)
					return nil
				},
				output: func() ([]byte, error) {
					return nil, errors.New("error: can't find in /etc/fstab foo bar testing")
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.MakeFilesystem(context.TODO())
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("filesystem is created if error is returned from mount command", func(t *testing.T) {
		var called bool
		var cmd CommanderProvider = func(ctx context.Context, name string, args ...string) Commander {
			return &mockCmd{
				run: func() error {
					// Expect the call from IsMounted here and just return what we need
					// to indicate that nothing is currently mounted.
					if name == "grep" {
						return &mockedExitCode{code: 1}
					}
					called = true
					assert.Equal(t, "mkfs", name)
					assert.Equal(t, []string{"-t", "ext4", "/disk.img"}, args)
					return nil
				},
				output: func() ([]byte, error) {
					if name == "mount" {
						return nil, &exec.ExitError{
							Stderr: []byte("foo bar: exit status 32\n"),
						}
					}
					return nil, nil
				},
			}
		}

		disk := newMockDisk(cmd)
		err := disk.MakeFilesystem(context.TODO())
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("error is returned if currently mounted", func(t *testing.T) {
		disk := newMockDisk(nil)
		err := disk.MakeFilesystem(context.TODO())
		assert.Error(t, err)
		assert.EqualError(t, err, ErrFilesystemExists.Error())
	})
}