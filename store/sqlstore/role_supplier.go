// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sqlstore

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/gorp"
	"github.com/mattermost/mattermost-server/v5/model"
	"github.com/mattermost/mattermost-server/v5/store"
)

type SqlRoleStore struct {
	SqlStore
}

type Role struct {
	Id            string
	Name          string
	DisplayName   string
	Description   string
	CreateAt      int64
	UpdateAt      int64
	DeleteAt      int64
	Permissions   string
	SchemeManaged bool
	BuiltIn       bool
}

type channelRolesPermissions struct {
	GuestRoleName                string
	UserRoleName                 string
	AdminRoleName                string
	HigherScopedGuestPermissions string
	HigherScopedUserPermissions  string
	HigherScopedAdminPermissions string
}

func NewRoleFromModel(role *model.Role) *Role {
	permissionsMap := make(map[string]bool)
	permissions := ""

	for _, permission := range role.Permissions {
		if !permissionsMap[permission] {
			permissions += fmt.Sprintf(" %v", permission)
			permissionsMap[permission] = true
		}
	}

	return &Role{
		Id:            role.Id,
		Name:          role.Name,
		DisplayName:   role.DisplayName,
		Description:   role.Description,
		CreateAt:      role.CreateAt,
		UpdateAt:      role.UpdateAt,
		DeleteAt:      role.DeleteAt,
		Permissions:   permissions,
		SchemeManaged: role.SchemeManaged,
		BuiltIn:       role.BuiltIn,
	}
}

func (role Role) ToModel() *model.Role {
	return &model.Role{
		Id:            role.Id,
		Name:          role.Name,
		DisplayName:   role.DisplayName,
		Description:   role.Description,
		CreateAt:      role.CreateAt,
		UpdateAt:      role.UpdateAt,
		DeleteAt:      role.DeleteAt,
		Permissions:   strings.Fields(role.Permissions),
		SchemeManaged: role.SchemeManaged,
		BuiltIn:       role.BuiltIn,
	}
}

func newSqlRoleStore(sqlStore SqlStore) store.RoleStore {
	s := &SqlRoleStore{sqlStore}

	for _, db := range sqlStore.GetAllConns() {
		table := db.AddTableWithName(Role{}, "Roles").SetKeys(false, "Id")
		table.ColMap("Id").SetMaxSize(26)
		table.ColMap("Name").SetMaxSize(64).SetUnique(true)
		table.ColMap("DisplayName").SetMaxSize(128)
		table.ColMap("Description").SetMaxSize(1024)
		table.ColMap("Permissions").SetMaxSize(4096)
	}
	return s
}

func (s *SqlRoleStore) Save(role *model.Role) (*model.Role, *model.AppError) {
	// Check the role is valid before proceeding.
	if !role.IsValidWithoutId() {
		return nil, model.NewAppError("SqlRoleStore.Save", "store.sql_role.save.invalid_role.app_error", nil, "", http.StatusBadRequest)
	}

	if len(role.Id) == 0 {
		transaction, err := s.GetMaster().Begin()
		if err != nil {
			return nil, model.NewAppError("SqlRoleStore.RoleSave", "store.sql_role.save.open_transaction.app_error", nil, err.Error(), http.StatusInternalServerError)
		}
		defer finalizeTransaction(transaction)
		createdRole, appErr := s.createRole(role, transaction)
		if appErr != nil {
			transaction.Rollback()
			return nil, appErr
		} else if err := transaction.Commit(); err != nil {
			return nil, model.NewAppError("SqlRoleStore.RoleSave", "store.sql_role.save_role.commit_transaction.app_error", nil, err.Error(), http.StatusInternalServerError)
		}
		return createdRole, nil
	}

	dbRole := NewRoleFromModel(role)
	dbRole.UpdateAt = model.GetMillis()
	if rowsChanged, err := s.GetMaster().Update(dbRole); err != nil {
		return nil, model.NewAppError("SqlRoleStore.Save", "store.sql_role.save.update.app_error", nil, err.Error(), http.StatusInternalServerError)
	} else if rowsChanged != 1 {
		return nil, model.NewAppError("SqlRoleStore.Save", "store.sql_role.save.update.app_error", nil, "no record to update", http.StatusInternalServerError)
	}

	return dbRole.ToModel(), nil
}

func (s *SqlRoleStore) createRole(role *model.Role, transaction *gorp.Transaction) (*model.Role, *model.AppError) {
	// Check the role is valid before proceeding.
	if !role.IsValidWithoutId() {
		return nil, model.NewAppError("SqlRoleStore.Save", "store.sql_role.save.invalid_role.app_error", nil, "", http.StatusBadRequest)
	}

	dbRole := NewRoleFromModel(role)

	dbRole.Id = model.NewId()
	dbRole.CreateAt = model.GetMillis()
	dbRole.UpdateAt = dbRole.CreateAt

	if err := transaction.Insert(dbRole); err != nil {
		return nil, model.NewAppError("SqlRoleStore.Save", "store.sql_role.save.insert.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	return dbRole.ToModel(), nil
}

func (s *SqlRoleStore) Get(roleId string) (*model.Role, *model.AppError) {
	var dbRole Role

	if err := s.GetReplica().SelectOne(&dbRole, "SELECT * from Roles WHERE Id = :Id", map[string]interface{}{"Id": roleId}); err != nil {
		if err == sql.ErrNoRows {
			return nil, model.NewAppError("SqlRoleStore.Get", "store.sql_role.get.app_error", nil, "Id="+roleId+", "+err.Error(), http.StatusNotFound)
		}
		return nil, model.NewAppError("SqlRoleStore.Get", "store.sql_role.get.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	return dbRole.ToModel(), nil
}

func (s *SqlRoleStore) GetAll() ([]*model.Role, *model.AppError) {
	var dbRoles []Role

	if _, err := s.GetReplica().Select(&dbRoles, "SELECT * from Roles", map[string]interface{}{}); err != nil {
		if err == sql.ErrNoRows {
			return nil, model.NewAppError("SqlRoleStore.GetAll", "store.sql_role.get_all.app_error", nil, err.Error(), http.StatusNotFound)
		}
		return nil, model.NewAppError("SqlRoleStore.GetAll", "store.sql_role.get_all.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	var roles []*model.Role
	for _, dbRole := range dbRoles {
		roles = append(roles, dbRole.ToModel())
	}
	return roles, nil
}

func (s *SqlRoleStore) GetByName(name string) (*model.Role, *model.AppError) {
	var dbRole Role

	if err := s.GetReplica().SelectOne(&dbRole, "SELECT * from Roles WHERE Name = :Name", map[string]interface{}{"Name": name}); err != nil {
		if err == sql.ErrNoRows {
			return nil, model.NewAppError("SqlRoleStore.GetByName", "store.sql_role.get_by_name.app_error", nil, "name="+name+",err="+err.Error(), http.StatusNotFound)
		}
		return nil, model.NewAppError("SqlRoleStore.GetByName", "store.sql_role.get_by_name.app_error", nil, "name="+name+",err="+err.Error(), http.StatusInternalServerError)
	}

	return dbRole.ToModel(), nil
}

func (s *SqlRoleStore) GetByNames(names []string) ([]*model.Role, *model.AppError) {
	var dbRoles []*Role

	if len(names) == 0 {
		return []*model.Role{}, nil
	}

	var searchPlaceholders []string
	var parameters = map[string]interface{}{}
	for i, value := range names {
		searchPlaceholders = append(searchPlaceholders, fmt.Sprintf(":Name%d", i))
		parameters[fmt.Sprintf("Name%d", i)] = value
	}

	searchTerm := "Name IN (" + strings.Join(searchPlaceholders, ", ") + ")"

	if _, err := s.GetReplica().Select(&dbRoles, "SELECT * from Roles WHERE "+searchTerm, parameters); err != nil {
		return nil, model.NewAppError("SqlRoleStore.GetByNames", "store.sql_role.get_by_names.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	var roles []*model.Role
	for _, dbRole := range dbRoles {
		roles = append(roles, dbRole.ToModel())
	}

	return roles, nil
}

func (s *SqlRoleStore) Delete(roleId string) (*model.Role, *model.AppError) {
	// Get the role.
	var role *Role
	if err := s.GetReplica().SelectOne(&role, "SELECT * from Roles WHERE Id = :Id", map[string]interface{}{"Id": roleId}); err != nil {
		if err == sql.ErrNoRows {
			return nil, model.NewAppError("SqlRoleStore.Delete", "store.sql_role.get.app_error", nil, "Id="+roleId+", "+err.Error(), http.StatusNotFound)
		}
		return nil, model.NewAppError("SqlRoleStore.Delete", "store.sql_role.get.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	time := model.GetMillis()
	role.DeleteAt = time
	role.UpdateAt = time

	if rowsChanged, err := s.GetMaster().Update(role); err != nil {
		return nil, model.NewAppError("SqlRoleStore.Delete", "store.sql_role.delete.update.app_error", nil, err.Error(), http.StatusInternalServerError)
	} else if rowsChanged != 1 {
		return nil, model.NewAppError("SqlRoleStore.Delete", "store.sql_role.delete.update.app_error", nil, "no record to update", http.StatusInternalServerError)
	}
	return role.ToModel(), nil
}

func (s *SqlRoleStore) PermanentDeleteAll() *model.AppError {
	if _, err := s.GetMaster().Exec("DELETE FROM Roles"); err != nil {
		return model.NewAppError("SqlRoleStore.PermanentDeleteAll", "store.sql_role.permanent_delete_all.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	return nil
}

func (s *SqlRoleStore) higherScopedPermissionsQuery(roleNames []string) string {
	sqlTmpl := `
		SELECT
			RoleSchemes.DefaultChannelGuestRole AS GuestRoleName,
			RoleSchemes.DefaultChannelUserRole AS UserRoleName,
			RoleSchemes.DefaultChannelAdminRole AS AdminRoleName,
			GuestRoles.Permissions AS HigherScopedGuestPermissions,
			UserRoles.Permissions AS HigherScopedUserPermissions,
			AdminRoles.Permissions AS HigherScopedAdminPermissions
		FROM
			Schemes AS RoleSchemes
			JOIN Channels ON Channels.SchemeId = RoleSchemes.Id
			JOIN Teams ON Teams.Id = Channels.TeamId
			JOIN Schemes ON Schemes.Id = Teams.SchemeId
			JOIN Roles AS GuestRoles ON GuestRoles.Name = Schemes.DefaultChannelGuestRole
			JOIN Roles AS UserRoles ON UserRoles.Name = Schemes.DefaultChannelUserRole
			JOIN Roles AS AdminRoles ON AdminRoles.Name = Schemes.DefaultChannelAdminRole
		WHERE
			RoleSchemes.DefaultChannelGuestRole IN ('%[1]s')
			OR RoleSchemes.DefaultChannelUserRole IN ('%[1]s')
			OR RoleSchemes.DefaultChannelAdminRole IN ('%[1]s')
		UNION
		SELECT
			Schemes.DefaultChannelGuestRole AS GuestRoleName,
			Schemes.DefaultChannelUserRole AS UserRoleName,
			Schemes.DefaultChannelAdminRole AS AdminRoleName,
			GuestRoles.Permissions AS HigherScopedGuestPermissions,
			UserRoles.Permissions AS HigherScopedUserPermissions,
			AdminRoles.Permissions AS HigherScopedAdminPermissions
		FROM
			Schemes
			JOIN Channels ON Channels.SchemeId = Schemes.Id
			JOIN Teams ON Teams.Id = Channels.TeamId
			JOIN Roles AS GuestRoles ON GuestRoles.Name = '%[2]s'
			JOIN Roles AS UserRoles ON UserRoles.Name = '%[3]s'
			JOIN Roles AS AdminRoles ON AdminRoles.Name = '%[4]s'
		WHERE
			(Schemes.DefaultChannelGuestRole IN ('%[1]s')
			OR Schemes.DefaultChannelUserRole IN ('%[1]s')
			OR Schemes.DefaultChannelAdminRole IN ('%[1]s'))
		AND (Teams.SchemeId = ''
			OR Teams.SchemeId IS NULL)
	`

	// The below three channel role names are referenced by their name value because there is no system scheme
	// record that ships with Mattermost, otherwise the system scheme would be referenced by name and the channel
	// roles would be referenced by their column names.
	return fmt.Sprintf(
		sqlTmpl,
		strings.Join(roleNames, "', '"),
		model.CHANNEL_GUEST_ROLE_ID,
		model.CHANNEL_USER_ROLE_ID,
		model.CHANNEL_ADMIN_ROLE_ID,
	)
}

func (s *SqlRoleStore) HigherScopedPermissions(roleNames []string) (map[string]*model.RolePermissions, *model.AppError) {
	sql := s.higherScopedPermissionsQuery(roleNames)

	var rolesPermissions []*channelRolesPermissions
	if _, err := s.GetReplica().Select(&rolesPermissions, sql); err != nil {
		return nil, model.NewAppError("SqlRoleStore.HigherScopedPermissions", "store.sql_role.get_by_names.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	roleNameHigherScopedPermissions := map[string]*model.RolePermissions{}

	for _, rp := range rolesPermissions {
		roleNameHigherScopedPermissions[rp.GuestRoleName] = &model.RolePermissions{RoleID: model.CHANNEL_GUEST_ROLE_ID, Permissions: strings.Split(rp.HigherScopedGuestPermissions, " ")}
		roleNameHigherScopedPermissions[rp.UserRoleName] = &model.RolePermissions{RoleID: model.CHANNEL_USER_ROLE_ID, Permissions: strings.Split(rp.HigherScopedUserPermissions, " ")}
		roleNameHigherScopedPermissions[rp.AdminRoleName] = &model.RolePermissions{RoleID: model.CHANNEL_ADMIN_ROLE_ID, Permissions: strings.Split(rp.HigherScopedAdminPermissions, " ")}
	}

	return roleNameHigherScopedPermissions, nil
}

func (s *SqlRoleStore) AllChannelSchemeRoles() ([]*model.Role, *model.AppError) {
	query := s.getQueryBuilder().
		Select("Roles.*").
		From("Schemes").
		Join("Roles ON Schemes.DefaultChannelGuestRole = Roles.Name OR Schemes.DefaultChannelUserRole = Roles.Name OR Schemes.DefaultChannelAdminRole = Roles.Name").
		Where(sq.Eq{"Schemes.Scope": model.SCHEME_SCOPE_CHANNEL}).
		Where(sq.Eq{"Roles.DeleteAt": 0}).
		Where(sq.Eq{"Schemes.DeleteAt": 0})

	queryString, args, err := query.ToSql()
	if err != nil {
		return nil, model.NewAppError("SqlRoleStore.AllChannelSchemeManagedRoles", "store.sql.build_query.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	var dbRoles []*Role
	if _, err = s.GetReplica().Select(&dbRoles, queryString, args...); err != nil {
		return nil, model.NewAppError("SqlRoleStore.AllChannelSchemeManagedRoles", "store.sql_role.get.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	var roles []*model.Role
	for _, dbRole := range dbRoles {
		roles = append(roles, dbRole.ToModel())
	}

	return roles, nil
}

// ChannelRolesUnderTeamRole finds all of the channel-scheme roles under the team of the given team-scheme role.
func (s *SqlRoleStore) ChannelRolesUnderTeamRole(roleName string) ([]*model.Role, *model.AppError) {
	query := s.getQueryBuilder().
		Select("ChannelSchemeRoles.*").
		From("Roles AS HigherScopedRoles").
		Join("Schemes AS HigherScopedSchemes ON (HigherScopedRoles.Name = HigherScopedSchemes.DefaultChannelGuestRole OR HigherScopedRoles.Name = HigherScopedSchemes.DefaultChannelUserRole OR HigherScopedRoles.Name = HigherScopedSchemes.DefaultChannelAdminRole)").
		Join("Teams ON Teams.SchemeId = HigherScopedSchemes.Id").
		Join("Channels ON Channels.TeamId = Teams.Id").
		Join("Schemes AS ChannelSchemes ON Channels.SchemeId = ChannelSchemes.Id").
		Join("Roles AS ChannelSchemeRoles ON (ChannelSchemeRoles.Name = ChannelSchemes.DefaultChannelGuestRole OR ChannelSchemeRoles.Name = ChannelSchemes.DefaultChannelUserRole OR ChannelSchemeRoles.Name = ChannelSchemes.DefaultChannelAdminRole)").
		Where(sq.Eq{"HigherScopedSchemes.Scope": model.SCHEME_SCOPE_TEAM}).
		Where(sq.Eq{"HigherScopedRoles.Name": roleName}).
		Where(sq.Eq{"HigherScopedRoles.DeleteAt": 0}).
		Where(sq.Eq{"HigherScopedSchemes.DeleteAt": 0}).
		Where(sq.Eq{"Teams.DeleteAt": 0}).
		Where(sq.Eq{"Channels.DeleteAt": 0}).
		Where(sq.Eq{"ChannelSchemes.DeleteAt": 0}).
		Where(sq.Eq{"ChannelSchemeRoles.DeleteAt": 0})

	queryString, args, err := query.ToSql()
	if err != nil {
		return nil, model.NewAppError("SqlRoleStore.ChannelRolesUnderTeamRole", "store.sql.build_query.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	var dbRoles []*Role
	if _, err = s.GetReplica().Select(&dbRoles, queryString, args...); err != nil {
		return nil, model.NewAppError("SqlRoleStore.ChannelRolesUnderTeamRole", "store.sql_role.get.app_error", nil, err.Error(), http.StatusInternalServerError)
	}

	var roles []*model.Role
	for _, dbRole := range dbRoles {
		roles = append(roles, dbRole.ToModel())
	}

	return roles, nil
}
