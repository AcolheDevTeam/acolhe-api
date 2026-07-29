package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/tenant"
)

func TestIdentityContextIsTypedAndImmutableAcrossParents(t *testing.T) {
	parent := context.Background()
	identity := tenant.Identity{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "psychologist",
	}
	child := tenant.WithIdentity(parent, identity)

	_, ok := tenant.FromContext(parent)
	assert.False(t, ok)
	got, ok := tenant.FromContext(child)
	require.True(t, ok)
	assert.Equal(t, identity, got)
	organizationID, err := tenant.OrgID(child)
	require.NoError(t, err)
	assert.Equal(t, identity.OrgID, organizationID)
}

func TestTenantContextFailsClosedWhenIdentityIsMissing(t *testing.T) {
	_, ok := tenant.FromContext(context.Background())
	assert.False(t, ok)
	organizationID, err := tenant.OrgID(context.Background())
	assert.Equal(t, uuid.Nil, organizationID)
	assert.ErrorIs(t, err, tenant.ErrNoTenant)
}
